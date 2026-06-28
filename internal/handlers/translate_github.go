package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"pogo.hails.cc/internal/i18n"
)

// GitHub sync: commits the merged locale files (embedded plus approved
// overrides) to a dedicated branch via the REST API and opens a pull request
// so approved translations can be pulled into the repo before the next build.
// Configured with GITHUB_TOKEN (fine-grained PAT: contents + pull requests
// read/write on the one repo) and GITHUB_REPO (owner/repo).

const (
	ghAPIBase    = "https://api.github.com"
	ghSyncBranch = "translations"
	ghBaseBranch = "main"
	ghLocalePath = "internal/i18n/locales"
)

var (
	ghSyncMu     sync.Mutex
	ghHTTPClient = &http.Client{Timeout: 30 * time.Second}
)

// ghRequest performs one GitHub API call and returns the status code and body.
func ghRequest(token, method, path string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, ghAPIBase+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, respBody, nil
}

// AdminTranslationsSync pushes every locale that has approved overrides to
// the sync branch and ensures a pull request exists. Only one sync runs at a
// time; concurrent requests get a 409.
func (h *Handlers) AdminTranslationsSync(w http.ResponseWriter, r *http.Request) {
	token := os.Getenv("GITHUB_TOKEN")
	repo := os.Getenv("GITHUB_REPO")
	if token == "" || repo == "" || !strings.Contains(repo, "/") {
		writeJSONError(w, h.t(r, "error.tl_gh_not_configured"), http.StatusBadRequest)
		return
	}
	if !ghSyncMu.TryLock() {
		writeJSONError(w, h.t(r, "error.tl_gh_sync_running"), http.StatusConflict)
		return
	}
	changed, prURL, step, err := syncTranslationsLocked(token, repo)
	ghSyncMu.Unlock()
	if err != nil {
		writeJSONError(w, h.t(r, "error.tl_gh_sync_failed")+" ("+step+")", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "files": changed, "pr_url": prURL})
}

// syncTranslationsToGitHub is the background entry point: it serializes on
// ghSyncMu (blocking, unlike the handler's TryLock) and runs the sync.
func syncTranslationsToGitHub(token, repo string) (changed []string, prURL, step string, err error) {
	ghSyncMu.Lock()
	defer ghSyncMu.Unlock()
	return syncTranslationsLocked(token, repo)
}

// syncTranslationsLocked performs the actual GitHub sync. The caller must hold
// ghSyncMu. On failure it returns a non-empty step naming where it stopped.
func syncTranslationsLocked(token, repo string) (changed []string, prURL, step string, err error) {
	owner, _, _ := strings.Cut(repo, "/")

	fail := func(step string, status int, body []byte, ferr error) (string, error) {
		if ferr != nil {
			log.Printf("github sync: %s: %v", step, ferr)
			return step, ferr
		}
		log.Printf("github sync: %s: status %d: %s", step, status, body)
		return step, fmt.Errorf("github sync %s: status %d", step, status)
	}

	status, body, err := ghRequest(token, "GET", "/repos/"+repo+"/git/ref/heads/"+ghBaseBranch, nil)
	if err != nil || status != http.StatusOK {
		step, err = fail("read base branch", status, body, err)
		return nil, "", step, err
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if json.Unmarshal(body, &ref) != nil || ref.Object.SHA == "" {
		step, err = fail("parse base branch", status, body, nil)
		return nil, "", step, err
	}
	baseSHA := ref.Object.SHA

	status, body, err = ghRequest(token, "GET",
		"/repos/"+repo+"/pulls?state=open&head="+owner+":"+ghSyncBranch, nil)
	if err != nil || status != http.StatusOK {
		step, err = fail("list pull requests", status, body, err)
		return nil, "", step, err
	}
	var prs []struct {
		HTMLURL string `json:"html_url"`
	}
	if json.Unmarshal(body, &prs) != nil {
		step, err = fail("parse pull requests", status, body, nil)
		return nil, "", step, err
	}
	if len(prs) > 0 {
		prURL = prs[0].HTMLURL
	}

	// Ensure the sync branch exists; without an open PR it is reset onto the
	// base branch so commits from merged or closed PRs do not pile up.
	status, body, err = ghRequest(token, "GET", "/repos/"+repo+"/git/ref/heads/"+ghSyncBranch, nil)
	switch {
	case err != nil:
		step, err = fail("read sync branch", status, body, err)
		return nil, "", step, err
	case status == http.StatusNotFound:
		status, body, err = ghRequest(token, "POST", "/repos/"+repo+"/git/refs",
			map[string]string{"ref": "refs/heads/" + ghSyncBranch, "sha": baseSHA})
		if err != nil || status != http.StatusCreated {
			step, err = fail("create sync branch", status, body, err)
			return nil, "", step, err
		}
	case status == http.StatusOK && prURL == "":
		status, body, err = ghRequest(token, "PATCH", "/repos/"+repo+"/git/refs/heads/"+ghSyncBranch,
			map[string]any{"sha": baseSHA, "force": true})
		if err != nil || status != http.StatusOK {
			step, err = fail("reset sync branch", status, body, err)
			return nil, "", step, err
		}
	case status != http.StatusOK:
		step, err = fail("read sync branch", status, body, nil)
		return nil, "", step, err
	}

	changed = []string{}
	for _, lang := range i18n.OverlayLangs() {
		data, merr := json.MarshalIndent(i18n.Bundle(lang), "", "  ")
		if merr != nil {
			step, err = fail("marshal "+lang, 0, nil, merr)
			return nil, "", step, err
		}
		data = append(data, '\n')
		filePath := ghLocalePath + "/" + lang + ".json"

		status, body, err = ghRequest(token, "GET",
			"/repos/"+repo+"/contents/"+filePath+"?ref="+ghSyncBranch, nil)
		blobSHA := ""
		if err == nil && status == http.StatusOK {
			var file struct {
				SHA     string `json:"sha"`
				Content string `json:"content"`
			}
			if json.Unmarshal(body, &file) == nil {
				blobSHA = file.SHA
				existing, decErr := base64.StdEncoding.DecodeString(
					strings.ReplaceAll(file.Content, "\n", ""))
				if decErr == nil && bytes.Equal(existing, data) {
					continue
				}
			}
		} else if err != nil || status != http.StatusNotFound {
			step, err = fail("read "+filePath, status, body, err)
			return nil, "", step, err
		}

		payload := map[string]any{
			"message": fmt.Sprintf("Sync approved %s translations", lang),
			"content": base64.StdEncoding.EncodeToString(data),
			"branch":  ghSyncBranch,
		}
		if blobSHA != "" {
			payload["sha"] = blobSHA
		}
		status, body, err = ghRequest(token, "PUT", "/repos/"+repo+"/contents/"+filePath, payload)
		if err != nil || (status != http.StatusOK && status != http.StatusCreated) {
			step, err = fail("commit "+filePath, status, body, err)
			return nil, "", step, err
		}
		changed = append(changed, lang+".json")
	}

	if len(changed) == 0 {
		return changed, prURL, "", nil
	}

	if prURL == "" {
		status, body, err = ghRequest(token, "POST", "/repos/"+repo+"/pulls", map[string]string{
			"title": "Sync approved translations",
			"head":  ghSyncBranch,
			"base":  ghBaseBranch,
			"body":  "Merged locale files exported from the live translation overrides: " + strings.Join(changed, ", "),
		})
		if err != nil || status != http.StatusCreated {
			step, err = fail("create pull request", status, body, err)
			return nil, "", step, err
		}
		var pr struct {
			HTMLURL string `json:"html_url"`
		}
		if json.Unmarshal(body, &pr) == nil {
			prURL = pr.HTMLURL
		}
	}

	return changed, prURL, "", nil
}

const (
	translationSyncStartup  = 1 * time.Minute
	translationSyncBackstop = 6 * time.Hour
	translationSyncDebounce = 30 * time.Second
)

// translationSyncCh coalesces approval-triggered sync requests. A buffer of one
// is enough: any queued request will sync the latest overrides for everyone.
var translationSyncCh = make(chan struct{}, 1)

// triggerTranslationSync asks the background syncer to mirror approved
// overrides to GitHub soon. Non-blocking; safe to call when sync is disabled.
func triggerTranslationSync() {
	select {
	case translationSyncCh <- struct{}{}:
	default:
	}
}

// StartTranslationAutoSync mirrors approved translation overrides to the GitHub
// sync branch automatically, so approved work is never stranded only on the
// server's disk (residual risk #2). It syncs shortly after startup, on a
// periodic backstop, and -- debounced -- whenever an edit is approved. It is a
// no-op when GitHub is not configured.
func (h *Handlers) StartTranslationAutoSync() {
	token := os.Getenv("GITHUB_TOKEN")
	repo := os.Getenv("GITHUB_REPO")
	if token == "" || repo == "" || !strings.Contains(repo, "/") {
		log.Printf("i18n auto-sync: GITHUB_TOKEN/GITHUB_REPO not configured; approved translations will NOT be mirrored to git")
		return
	}
	go func() {
		startup := time.NewTimer(translationSyncStartup)
		backstop := time.NewTicker(translationSyncBackstop)
		defer backstop.Stop()
		run := func(reason string) {
			changed, prURL, step, err := syncTranslationsToGitHub(token, repo)
			if err != nil {
				log.Printf("i18n auto-sync (%s): %s: %v", reason, step, err)
				return
			}
			if len(changed) > 0 {
				log.Printf("i18n auto-sync (%s): mirrored %v -> %s", reason, changed, prURL)
			}
		}
		for {
			select {
			case <-startup.C:
				run("startup")
			case <-backstop.C:
				run("backstop")
			case <-translationSyncCh:
				// Debounce: fold a burst of approvals into a single sync.
				d := time.NewTimer(translationSyncDebounce)
			debounce:
				for {
					select {
					case <-translationSyncCh:
						if !d.Stop() {
							select {
							case <-d.C:
							default:
							}
						}
						d.Reset(translationSyncDebounce)
					case <-d.C:
						break debounce
					}
				}
				run("approval")
			}
		}
	}()
}
