package handlers

// Mirrors costume labels named through the admin panel back into the repo.
//
// A name added in the admin panel lives in an overlay file on the server. That makes it live
// immediately, but it also means it exists nowhere else: if the box is rebuilt, or somebody runs
// `make costumes` and commits, the name is lost or fought over. So the merged label set is PR'd
// back into internal/costumes/labels.json, and once that merges the embedded file carries the name
// and the overlay entry becomes redundant-but-identical.
//
// This is the same closing-of-the-loop that translate_github.go does for approved translations,
// and it reuses that file's ghRequest / ghBaseBranch.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"pogo.hails.cc/internal/costumes"
)

const (
	ghCostumeBranch = "costume-labels"
	ghCostumePath   = "internal/costumes/labels.json"

	costumeSyncStartup  = 2 * time.Minute
	costumeSyncBackstop = 12 * time.Hour
	costumeSyncDebounce = 30 * time.Second
)

var (
	costumeSyncMu sync.Mutex
	costumeSyncCh = make(chan struct{}, 1)
)

// triggerCostumeSync asks the background syncer to mirror the labels soon. Non-blocking, and a
// no-op when GitHub is not configured.
func triggerCostumeSync() {
	select {
	case costumeSyncCh <- struct{}{}:
	default:
	}
}

// syncCostumeLabels pushes the merged labels onto the sync branch and opens a PR if none is open.
// Returns changed=false when the file on the branch already matches, so a quiet run stays quiet.
func syncCostumeLabels(token, repo string) (changed bool, prURL, step string, err error) {
	costumeSyncMu.Lock()
	defer costumeSyncMu.Unlock()

	owner, _, _ := strings.Cut(repo, "/")

	fail := func(step string, status int, body []byte, ferr error) (string, error) {
		if ferr != nil {
			log.Printf("costume sync: %s: %v", step, ferr)
			return step, ferr
		}
		log.Printf("costume sync: %s: status %d: %s", step, status, body)
		return step, fmt.Errorf("costume sync %s: status %d", step, status)
	}

	// The merged set: embedded labels plus everything named in the admin panel. The _comment blocks
	// in labels.json ride through as raw JSON, so this does not quietly delete the notes in it.
	data, merr := costumes.LabelsJSON()
	if merr != nil {
		step, err = fail("marshal labels", 0, nil, merr)
		return false, "", step, err
	}
	// LabelsJSON reproduces the committed file, which already ends in a newline; only add one if a
	// future edit drops it. Appending unconditionally would push a one-byte diff on every run.
	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}

	status, body, err := ghRequest(token, "GET", "/repos/"+repo+"/git/ref/heads/"+ghBaseBranch, nil)
	if err != nil || status != http.StatusOK {
		step, err = fail("read base branch", status, body, err)
		return false, "", step, err
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if json.Unmarshal(body, &ref) != nil || ref.Object.SHA == "" {
		step, err = fail("parse base branch", status, body, nil)
		return false, "", step, err
	}
	baseSHA := ref.Object.SHA

	status, body, err = ghRequest(token, "GET",
		"/repos/"+repo+"/pulls?state=open&head="+owner+":"+ghCostumeBranch, nil)
	if err != nil || status != http.StatusOK {
		step, err = fail("list pull requests", status, body, err)
		return false, "", step, err
	}
	var prs []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	prNum := 0
	if json.Unmarshal(body, &prs) == nil && len(prs) > 0 {
		prNum, prURL = prs[0].Number, prs[0].HTMLURL
	}

	// Nothing to sync is decided against BASE, not against the branch: the question is whether main
	// already carries every name we have. Asking the branch instead would call a branch that already
	// holds our commit "up to date" and never open a PR for it.
	baseContent, _, berr := costumeFileOn(token, repo, ghBaseBranch)
	if berr != nil {
		step, err = fail("read "+ghCostumePath+" on "+ghBaseBranch, 0, nil, berr)
		return false, "", step, err
	}
	if bytes.Equal(baseContent, data) {
		if prNum != 0 {
			closeEmptyCostumePR(token, repo, prNum)
		} else {
			deleteCostumeBranch(token, repo)
		}
		return false, "", "", nil
	}

	// Create the branch, or fast-forward it onto base when it has fallen behind. A branch left
	// sitting on an old base is one merge to main away from an unresolvable conflict; that is what
	// happened to PR #1.
	//
	// But reset ONLY when it is actually stale. A reset makes the branch momentarily identical to
	// base, and GitHub closes any PR whose head matches its base, so resetting a branch that was
	// already current would throw the PR away and orphan the commit pushed just below. Being one
	// commit on top of the current base IS current: nothing to do.
	status, body, err = ghRequest(token, "GET", "/repos/"+repo+"/git/ref/heads/"+ghCostumeBranch, nil)
	switch {
	case err == nil && status == http.StatusNotFound:
		status, body, err = ghRequest(token, "POST", "/repos/"+repo+"/git/refs",
			map[string]string{"ref": "refs/heads/" + ghCostumeBranch, "sha": baseSHA})
		if err != nil || status != http.StatusCreated {
			step, err = fail("create branch", status, body, err)
			return false, "", step, err
		}
	case err == nil && status == http.StatusOK:
		if json.Unmarshal(body, &ref) != nil || ref.Object.SHA == "" {
			step, err = fail("parse branch", status, body, nil)
			return false, "", step, err
		}
		if onBase, berr := costumeBranchOnBase(token, repo, ref.Object.SHA, baseSHA); berr != nil {
			step, err = fail("read branch commit", 0, nil, berr)
			return false, "", step, err
		} else if !onBase {
			status, body, err = ghRequest(token, "PATCH", "/repos/"+repo+"/git/refs/heads/"+ghCostumeBranch,
				map[string]any{"sha": baseSHA, "force": true})
			if err != nil || status != http.StatusOK {
				step, err = fail("reset branch", status, body, err)
				return false, "", step, err
			}
			// The reset just closed any open PR out from under us.
			prNum, prURL = 0, ""
		}
	default:
		step, err = fail("read branch", status, body, err)
		return false, "", step, err
	}

	// Main does not have our content, so the branch must. Commit unless it already matches, which
	// keeps a quiet run from force-pushing an identical commit and churning the PR.
	branchContent, blobSHA, berr := costumeFileOn(token, repo, ghCostumeBranch)
	if berr != nil {
		step, err = fail("read "+ghCostumePath, 0, nil, berr)
		return false, "", step, err
	}
	changed = !bytes.Equal(branchContent, data)
	if changed {
		payload := map[string]any{
			"message": "Sync costume labels named in the admin panel",
			"content": base64.StdEncoding.EncodeToString(data),
			"branch":  ghCostumeBranch,
		}
		if blobSHA != "" {
			payload["sha"] = blobSHA
		}
		status, body, err = ghRequest(token, "PUT", "/repos/"+repo+"/contents/"+ghCostumePath, payload)
		if err != nil || (status != http.StatusOK && status != http.StatusCreated) {
			step, err = fail("commit "+ghCostumePath, status, body, err)
			return false, "", step, err
		}
	}

	// Re-check rather than trust the read from before the reset: if the branch was stale, resetting
	// it closed the PR, and the commit above would otherwise sit on the branch with nothing pointing
	// at it. Asking again is one request and cannot be stale.
	if prURL == "" {
		status, body, err = ghRequest(token, "GET",
			"/repos/"+repo+"/pulls?state=open&head="+owner+":"+ghCostumeBranch, nil)
		if err == nil && status == http.StatusOK && json.Unmarshal(body, &prs) == nil && len(prs) > 0 {
			prNum, prURL = prs[0].Number, prs[0].HTMLURL
		}
	}
	if prURL == "" {
		status, body, err = ghRequest(token, "POST", "/repos/"+repo+"/pulls", map[string]string{
			"title": "Sync costume labels",
			"head":  ghCostumeBranch,
			"base":  ghBaseBranch,
			"body": "Costume labels named through the admin panel, merged into the embedded set.\n\n" +
				"They are already live on the site (an overlay file on the server); merging this is " +
				"what makes them survive a rebuild.",
		})
		if err != nil || status != http.StatusCreated {
			step, err = fail("create pull request", status, body, err)
			return false, "", step, err
		}
		var pr struct {
			HTMLURL string `json:"html_url"`
		}
		if json.Unmarshal(body, &pr) == nil {
			prURL = pr.HTMLURL
		}
		changed = true // a PR that did not exist a moment ago is worth reporting even if the
		// branch already carried the content, which is how a PR closed out from
		// under a good commit gets one again.
	}
	return changed, prURL, "", nil
}

// costumeFileOn reads labels.json off a ref, returning its bytes and blob SHA. A ref that does not
// have the file yet is not an error: it reads as absent (nil, "", nil), which compares unequal to
// anything we would push.
func costumeFileOn(token, repo, ref string) (content []byte, blobSHA string, err error) {
	status, body, err := ghRequest(token, "GET",
		"/repos/"+repo+"/contents/"+ghCostumePath+"?ref="+ref, nil)
	if err != nil {
		return nil, "", err
	}
	if status == http.StatusNotFound {
		return nil, "", nil
	}
	if status != http.StatusOK {
		return nil, "", fmt.Errorf("read %s on %s: status %d", ghCostumePath, ref, status)
	}
	var file struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, "", err
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return nil, "", fmt.Errorf("decode %s on %s: %w", ghCostumePath, ref, err)
	}
	return b, file.SHA, nil
}

// deleteCostumeBranch drops the sync branch when there is no PR to close alongside it. Best-effort,
// and a branch that is already gone is the desired state, not a failure.
func deleteCostumeBranch(token, repo string) {
	status, body, err := ghRequest(token, "DELETE",
		"/repos/"+repo+"/git/refs/heads/"+ghCostumeBranch, nil)
	if err != nil || (status != http.StatusNoContent && status != http.StatusNotFound) {
		log.Printf("costume sync: delete stale branch: status %d: %s: %v", status, body, err)
	}
}

// costumeBranchOnBase reports whether the sync branch is already sitting directly on base, i.e. is
// the single sync commit on top of it, or is base itself. Anything else (a stale base, a branch
// somebody else pushed to) counts as stale and wants a reset.
func costumeBranchOnBase(token, repo, branchSHA, baseSHA string) (bool, error) {
	if branchSHA == baseSHA {
		return true, nil
	}
	status, body, err := ghRequest(token, "GET", "/repos/"+repo+"/git/commits/"+branchSHA, nil)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("read commit %s: status %d", branchSHA, status)
	}
	var commit struct {
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if err := json.Unmarshal(body, &commit); err != nil {
		return false, err
	}
	return len(commit.Parents) == 1 && commit.Parents[0].SHA == baseSHA, nil
}

// closeEmptyCostumePR closes a sync PR that has nothing left to merge, and drops the branch with it.
// The next run recreates both from current main the moment a name actually needs mirroring.
//
// Best-effort: failing to tidy up is not worth failing the sync over, so this only logs. The PR
// being left open is untidy, not broken.
func closeEmptyCostumePR(token, repo string, num int) {
	status, body, err := ghRequest(token, "PATCH",
		fmt.Sprintf("/repos/%s/pulls/%d", repo, num), map[string]string{"state": "closed"})
	if err != nil || status != http.StatusOK {
		log.Printf("costume sync: close empty PR #%d: status %d: %s: %v", num, status, body, err)
		return
	}
	status, body, err = ghRequest(token, "DELETE", "/repos/"+repo+"/git/refs/heads/"+ghCostumeBranch, nil)
	if err != nil || (status != http.StatusNoContent && status != http.StatusNotFound) {
		log.Printf("costume sync: delete branch after closing PR #%d: status %d: %s: %v",
			num, status, body, err)
		return
	}
	log.Printf("costume sync: closed PR #%d; main already carries every name we have", num)
}

// StartCostumeAutoSync mirrors admin-named costume labels to GitHub, so a name is never stranded
// only on the server's disk. No-op when GitHub is not configured.
func (h *Handlers) StartCostumeAutoSync() {
	token := os.Getenv("GITHUB_TOKEN")
	repo := os.Getenv("GITHUB_REPO")
	if token == "" || repo == "" || !strings.Contains(repo, "/") {
		log.Printf("costume auto-sync: GITHUB_TOKEN/GITHUB_REPO not configured; costume labels named in the admin panel will NOT be mirrored to git")
		return
	}
	go func() {
		startup := time.NewTimer(costumeSyncStartup)
		backstop := time.NewTicker(costumeSyncBackstop)
		defer backstop.Stop()

		run := func(reason string) {
			changed, prURL, step, err := syncCostumeLabels(token, repo)
			if err != nil {
				log.Printf("costume auto-sync (%s): %s: %v", reason, step, err)
				return
			}
			if changed {
				log.Printf("costume auto-sync (%s): mirrored labels -> %s", reason, prURL)
			}
		}

		for {
			select {
			case <-startup.C:
				run("startup")
			case <-backstop.C:
				run("backstop")
			case <-costumeSyncCh:
				// Debounce: fold a burst of namings into one PR push.
				d := time.NewTimer(costumeSyncDebounce)
			debounce:
				for {
					select {
					case <-costumeSyncCh:
						if !d.Stop() {
							<-d.C
						}
						d.Reset(costumeSyncDebounce)
					case <-d.C:
						break debounce
					}
				}
				run("named")
			}
		}
	}()
}
