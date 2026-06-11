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
		writeJSONError(w, "github sync is not configured", http.StatusBadRequest)
		return
	}
	if !ghSyncMu.TryLock() {
		writeJSONError(w, "a sync is already running", http.StatusConflict)
		return
	}
	defer ghSyncMu.Unlock()

	owner, _, _ := strings.Cut(repo, "/")

	fail := func(step string, status int, body []byte, err error) {
		if err != nil {
			log.Printf("github sync: %s: %v", step, err)
		} else {
			log.Printf("github sync: %s: status %d: %s", step, status, body)
		}
		writeJSONError(w, "github sync failed at "+step, http.StatusBadGateway)
	}

	// Base branch head.
	status, body, err := ghRequest(token, "GET", "/repos/"+repo+"/git/ref/heads/"+ghBaseBranch, nil)
	if err != nil || status != http.StatusOK {
		fail("read base branch", status, body, err)
		return
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if json.Unmarshal(body, &ref) != nil || ref.Object.SHA == "" {
		fail("parse base branch", status, body, nil)
		return
	}
	baseSHA := ref.Object.SHA

	// Existing open sync PR, if any.
	status, body, err = ghRequest(token, "GET",
		"/repos/"+repo+"/pulls?state=open&head="+owner+":"+ghSyncBranch, nil)
	if err != nil || status != http.StatusOK {
		fail("list pull requests", status, body, err)
		return
	}
	var prs []struct {
		HTMLURL string `json:"html_url"`
	}
	if json.Unmarshal(body, &prs) != nil {
		fail("parse pull requests", status, body, nil)
		return
	}
	prURL := ""
	if len(prs) > 0 {
		prURL = prs[0].HTMLURL
	}

	// Ensure the sync branch exists; without an open PR it is reset onto the
	// base branch so commits from merged or closed PRs do not pile up.
	status, body, err = ghRequest(token, "GET", "/repos/"+repo+"/git/ref/heads/"+ghSyncBranch, nil)
	switch {
	case err != nil:
		fail("read sync branch", status, body, err)
		return
	case status == http.StatusNotFound:
		status, body, err = ghRequest(token, "POST", "/repos/"+repo+"/git/refs",
			map[string]string{"ref": "refs/heads/" + ghSyncBranch, "sha": baseSHA})
		if err != nil || status != http.StatusCreated {
			fail("create sync branch", status, body, err)
			return
		}
	case status == http.StatusOK && prURL == "":
		status, body, err = ghRequest(token, "PATCH", "/repos/"+repo+"/git/refs/heads/"+ghSyncBranch,
			map[string]any{"sha": baseSHA, "force": true})
		if err != nil || status != http.StatusOK {
			fail("reset sync branch", status, body, err)
			return
		}
	case status != http.StatusOK:
		fail("read sync branch", status, body, nil)
		return
	}

	// Commit each locale that has approved overrides, skipping files whose
	// content on the branch already matches.
	changed := []string{}
	for _, lang := range i18n.OverlayLangs() {
		data, err := json.MarshalIndent(i18n.Bundle(lang), "", "  ")
		if err != nil {
			fail("marshal "+lang, 0, nil, err)
			return
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
			fail("read "+filePath, status, body, err)
			return
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
			fail("commit "+filePath, status, body, err)
			return
		}
		changed = append(changed, lang+".json")
	}

	w.Header().Set("Content-Type", "application/json")
	if len(changed) == 0 {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "files": changed, "pr_url": prURL})
		return
	}

	if prURL == "" {
		status, body, err = ghRequest(token, "POST", "/repos/"+repo+"/pulls", map[string]string{
			"title": "Sync approved translations",
			"head":  ghSyncBranch,
			"base":  ghBaseBranch,
			"body":  "Merged locale files exported from the live translation overrides: " + strings.Join(changed, ", "),
		})
		if err != nil || status != http.StatusCreated {
			fail("create pull request", status, body, err)
			return
		}
		var pr struct {
			HTMLURL string `json:"html_url"`
		}
		if json.Unmarshal(body, &pr) == nil {
			prURL = pr.HTMLURL
		}
	}

	json.NewEncoder(w).Encode(map[string]any{"ok": true, "files": changed, "pr_url": prURL})
}
