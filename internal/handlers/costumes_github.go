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
	data = append(data, '\n')

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
		HTMLURL string `json:"html_url"`
	}
	if json.Unmarshal(body, &prs) == nil && len(prs) > 0 {
		prURL = prs[0].HTMLURL
	}

	// Create the branch, or fast-forward it onto base when no PR is open (so a merged PR does not
	// leave the branch stranded behind main).
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
		if prURL == "" {
			status, body, err = ghRequest(token, "PATCH", "/repos/"+repo+"/git/refs/heads/"+ghCostumeBranch,
				map[string]any{"sha": baseSHA, "force": true})
			if err != nil || status != http.StatusOK {
				step, err = fail("reset branch", status, body, err)
				return false, "", step, err
			}
		}
	default:
		step, err = fail("read branch", status, body, err)
		return false, "", step, err
	}

	status, body, err = ghRequest(token, "GET",
		"/repos/"+repo+"/contents/"+ghCostumePath+"?ref="+ghCostumeBranch, nil)
	blobSHA := ""
	if err == nil && status == http.StatusOK {
		var file struct {
			SHA     string `json:"sha"`
			Content string `json:"content"`
		}
		if json.Unmarshal(body, &file) == nil {
			blobSHA = file.SHA
			existing, decErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
			if decErr == nil && bytes.Equal(existing, data) {
				return false, prURL, "", nil // already up to date
			}
		}
	} else if err != nil || status != http.StatusNotFound {
		step, err = fail("read "+ghCostumePath, status, body, err)
		return false, "", step, err
	}

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
	}
	return true, prURL, "", nil
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
