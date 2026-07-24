package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"time"
)

//go:embed web
var webFS embed.FS

// runServe starts an HTTP server that serves the web review UI and exposes
// two JSON endpoints backed by the same COS logic as the CLI:
//
//	GET  /api/plan?date=YYYY-MM-DD  -> today's review plan (words + status)
//	POST /api/record                -> apply results, bump version, upload
//
// The frontend static assets are embedded into the binary via go:embed, so a
// single executable is fully self-contained (works offline on an intranet).
func runServe(fs_ *flag.FlagSet, lang string) {
	addr := fs_.String("addr", "127.0.0.1", "Bind address (use 0.0.0.0 to expose on the network)")
	port := fs_.Int("port", 8080, "Port to listen on")
	fs_.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	// Static frontend from embedded web/ dir (strip the "web" prefix).
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error mounting web assets: %v\n", err)
		os.Exit(1)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("/api/plan", func(w http.ResponseWriter, r *http.Request) {
		handlePlan(w, r, storage, lang)
	})
	mux.HandleFunc("/api/record", func(w http.ResponseWriter, r *http.Request) {
		handleRecord(w, r, storage, lang)
	})

	listen := fmt.Sprintf("%s:%d", *addr, *port)
	fmt.Fprintf(os.Stderr, "jrp serve (%s) listening on http://%s\n", lang, listen)
	if err := http.ListenAndServe(listen, mux); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// handlePlan returns the review plan for the target date. It downloads the
// latest archive, initializes today's v1.0 if the latest is from a previous
// day, builds the due-word plan, uploads the plan JSON (so a subsequent
// /api/record can resolve numbers), and returns it.
func handlePlan(w http.ResponseWriter, r *http.Request, storage *Storage, lang string) {
	ctx := context.Background()

	dateStr := r.URL.Query().Get("date")
	var targetDate time.Time
	var err error
	if dateStr != "" {
		targetDate, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false, "error": "invalid date, expected YYYY-MM-DD",
			})
			return
		}
	} else {
		targetDate = time.Now()
	}

	data, archiveFilename, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("download archive: %v", err),
		})
		return
	}

	arc, err := ParseArchive(string(data), lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("parse archive: %v", err),
		})
		return
	}

	// If the latest archive predates the target day, initialize today's v1.0.
	arcDate := targetDate
	if d, _, _, perr := ParseFilename(archiveFilename); perr == nil {
		arcDate = d
	}
	if !sameDay(arcDate, targetDate) {
		AddChangelogEntry(arc, targetDate, 1, 0, "新日初始化（serve）")
		newFilename := ArchiveFilename(lang, targetDate, 1, 0)
		if err := storage.UploadArchive(ctx, newFilename, []byte(WriteArchive(arc))); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": fmt.Sprintf("init today archive: %v", err),
			})
			return
		}
	}

	plan := BuildDuePlan(arc, lang, targetDate)

	// Persist the plan JSON so /api/record can map numbers → words.
	if err := storage.UploadPlan(ctx, plan); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("upload plan: %v", err),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"date":      plan.Date,
		"due_count": len(plan.Words),
		"words":     plan.Words,
	})
}

// handleRecord applies review results submitted from the web UI.
func handleRecord(w http.ResponseWriter, r *http.Request, storage *Storage, lang string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	var input RecordInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("bad body: %v", err),
		})
		return
	}
	if input.Language == "" {
		input.Language = lang
	}

	result, err := ApplyRecord(context.Background(), storage, lang, input)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, result)
}
