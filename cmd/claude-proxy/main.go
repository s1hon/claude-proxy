// Command claude-proxy is an OpenAI-compatible HTTP proxy that drives the
// Claude Code CLI as a backend.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/s1hon/claude-proxy/internal/config"
	"github.com/s1hon/claude-proxy/internal/server"
	"github.com/s1hon/claude-proxy/internal/session"
	"github.com/s1hon/claude-proxy/internal/stats"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	cfg := config.Load()

	store := session.NewStore(cfg.StatePath)
	if err := store.Load(sessionFileExists); err != nil {
		log.Printf("[main] state load: %v", err)
	}

	h := server.NewHandler(server.Deps{
		Config:  cfg,
		Store:   store,
		Stats:   stats.New(),
		Limiter: server.NewLimiter(cfg.MaxGlobal, cfg.MaxPerChannel),
	})

	srv := server.Build(h, cfg.APIPort, cfg.StatusPort)
	errs := srv.Start()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("[main] signal %s — shutting down", sig)
	case err := <-errs:
		log.Printf("[main] listener error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[main] shutdown: %v", err)
	}
	if err := store.Save(); err != nil {
		log.Printf("[main] state save: %v", err)
	}
	log.Printf("[main] bye")
}

// sessionFileExists checks whether the CLI session file exists on disk so we
// can prune stale entries on load. macOS uses /private/tmp as the canonical
// path for /tmp, so we construct the expected Claude session directory name.
func sessionFileExists(id string) bool {
	if id == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return true // fail open — keep the entry
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		tmp = os.TempDir()
	}
	// Claude stores sessions under ~/.claude/projects/<escaped-cwd>/<id>.jsonl.
	// The escaping replaces slashes with dashes.
	escaped := filepath.ToSlash(tmp)
	projectDir := "-" + filepath.Base(filepath.Dir(escaped)) + "-" + filepath.Base(escaped)
	path := filepath.Join(home, ".claude", "projects", projectDir, id+".jsonl")
	_, err = os.Stat(path)
	return err == nil
}
