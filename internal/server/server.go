package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jelmersnoeck/forge/internal/engine"
	"github.com/jelmersnoeck/forge/pkg/config"
)

// Server is the Forge HTTP server. It exposes a REST API, webhook endpoints,
// and SSE streaming for job progress.
type Server struct {
	engine *engine.Engine
	config *config.ServerConfig
	logger *slog.Logger
	jobs   *JobQueue
	broker *SSEBroker
	limiter *rateLimiter
}

// New creates a new Server wired with the engine, config, and logger.
func New(eng *engine.Engine, cfg *config.ServerConfig, logger *slog.Logger) *Server {
	broker := NewSSEBroker()
	queue := NewJobQueue(broker)

	s := &Server{
		engine:  eng,
		config:  cfg,
		logger:  logger,
		jobs:    queue,
		broker:  broker,
		limiter: newRateLimiter(100, time.Minute), // 100 requests/min per IP.
	}

	// Register job handlers that delegate to the engine.
	queue.RegisterHandler(JobTypeBuild, s.buildJobHandler)
	queue.RegisterHandler(JobTypeReview, s.reviewJobHandler)
	queue.RegisterHandler(JobTypePlan, s.planJobHandler)
	queue.RegisterHandler(JobTypeFeedback, s.feedbackJobHandler)

	return s
}

// Start starts the HTTP server and blocks until ctx is cancelled.
// It handles graceful shutdown when the context is done.
func (s *Server) Start(ctx context.Context) error {
	// Start the background job worker.
	go s.jobs.Run(ctx)

	addr := fmt.Sprintf(":%d", s.config.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.routes(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // Longer for SSE.
		IdleTimeout:  60 * time.Second,
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
	}

	// Start listening.
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("server listen: %w", err)
		}
		close(errCh)
	}()

	// Wait for shutdown signal.
	select {
	case <-ctx.Done():
		s.logger.Info("shutting down server")
		s.limiter.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

// routes builds the HTTP mux with all routes and middleware.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Health check.
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// REST API.
	mux.HandleFunc("POST /api/v1/build", s.handleBuild)
	mux.HandleFunc("POST /api/v1/review", s.handleReview)
	mux.HandleFunc("POST /api/v1/plan", s.handlePlan)
	mux.HandleFunc("POST /api/v1/feedback", s.handleFeedback)
	mux.HandleFunc("GET /api/v1/jobs", s.handleListJobs)
	// Job detail and stream use path prefix matching.
	mux.HandleFunc("GET /api/v1/jobs/", s.routeJobSubpath)

	// Webhooks.
	mux.HandleFunc("POST /webhooks/github", s.handleGitHubWebhook)

	// Apply middleware (outermost first).
	var handler http.Handler = mux
	handler = s.limiter.middleware(handler)
	handler = corsMiddleware(s.config.AllowedOrigins)(handler)
	handler = loggingMiddleware(s.logger)(handler)
	handler = panicRecovery(s.logger)(handler)
	handler = requestIDMiddleware(handler)

	return handler
}

// routeJobSubpath routes /api/v1/jobs/{id} and /api/v1/jobs/{id}/stream
// to the appropriate handler.
func (s *Server) routeJobSubpath(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if len(path) > len("/api/v1/jobs/") && hasSuffix(path, "/stream") {
		s.handleJobStream(w, r)
		return
	}
	s.handleGetJob(w, r)
}

// handleHealthz responds with 200 OK for health checks.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// hasSuffix returns true if s ends with suffix.
func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// --- Job handlers that bridge the queue to the engine ---

func (s *Server) buildJobHandler(ctx context.Context, job *Job) (interface{}, error) {
	// Decode the request. It may be a BuildAPIRequest or a generic map from webhooks.
	var issueRef, workDir, baseBranch string
	var principleSets []string

	switch req := job.Request.(type) {
	case BuildAPIRequest:
		issueRef = req.Issue
		workDir = req.WorkDir
		baseBranch = req.BaseBranch
		principleSets = req.PrincipleSets
	case map[string]interface{}:
		issueRef, _ = req["issue"].(string)
		workDir, _ = req["work_dir"].(string)
		baseBranch, _ = req["base_branch"].(string)
	default:
		return nil, fmt.Errorf("unexpected request type for build job")
	}

	if issueRef == "" {
		return nil, fmt.Errorf("issue reference is required")
	}

	s.jobs.AddLog(job.ID, fmt.Sprintf("starting build for %s", issueRef))

	result, err := s.engine.Build(ctx, engine.BuildRequest{
		IssueRef:      issueRef,
		PrincipleSets: principleSets,
		WorkDir:       workDir,
		BaseBranch:    baseBranch,
	})
	if err != nil {
		s.jobs.AddLog(job.ID, fmt.Sprintf("build failed: %v", err))
		return nil, err
	}

	s.jobs.AddLog(job.ID, fmt.Sprintf("build completed: status=%s, iterations=%d", result.Status, result.Iterations))
	return result, nil
}

func (s *Server) reviewJobHandler(ctx context.Context, job *Job) (interface{}, error) {
	switch req := job.Request.(type) {
	case ReviewAPIRequest:
		return s.engine.Review(ctx, engine.ReviewRequest{
			Diff:          req.Diff,
			PrincipleSets: req.PrincipleSets,
			WorkDir:       req.WorkDir,
		})
	case map[string]interface{}:
		// From webhook — PR review requires fetching the diff, which the engine
		// does not currently support via ref. Log and return a placeholder.
		s.jobs.AddLog(job.ID, "webhook-triggered review: fetching PR diff not yet implemented")
		return nil, fmt.Errorf("webhook-triggered PR review not yet implemented")
	default:
		return nil, fmt.Errorf("unexpected request type for review job")
	}
}

func (s *Server) planJobHandler(ctx context.Context, job *Job) (interface{}, error) {
	req, ok := job.Request.(PlanAPIRequest)
	if !ok {
		return nil, fmt.Errorf("unexpected request type for plan job")
	}

	return s.engine.Plan(ctx, engine.PlanRequest{
		IssueRef:      req.Issue,
		PrincipleSets: req.PrincipleSets,
		WorkDir:       req.WorkDir,
	})
}

func (s *Server) feedbackJobHandler(ctx context.Context, job *Job) (interface{}, error) {
	var feedbackReq engine.FeedbackRequest

	switch req := job.Request.(type) {
	case FeedbackAPIRequest:
		feedbackReq = engine.FeedbackRequest{
			PRNumber:      req.PRNumber,
			RepoFullName:  req.RepoFullName,
			ReviewBody:    req.ReviewBody,
			Comments:      req.Comments,
			WorkDir:       req.WorkDir,
			PrincipleSets: req.PrincipleSets,
		}
	case map[string]interface{}:
		// From webhook — extract fields from the generic map.
		if n, ok := req["pr_number"].(float64); ok {
			feedbackReq.PRNumber = int(n)
		} else if n, ok := req["pr_number"].(int); ok {
			feedbackReq.PRNumber = n
		}
		feedbackReq.RepoFullName, _ = req["repo_full_name"].(string)
		feedbackReq.ReviewBody, _ = req["review_body"].(string)
		feedbackReq.WorkDir, _ = req["work_dir"].(string)

		if comments, ok := req["comments"].([]engine.ReviewComment); ok {
			feedbackReq.Comments = comments
		}
	default:
		return nil, fmt.Errorf("unexpected request type for feedback job")
	}

	if feedbackReq.PRNumber == 0 {
		return nil, fmt.Errorf("pr_number is required")
	}

	s.jobs.AddLog(job.ID, fmt.Sprintf("starting feedback for PR #%d in %s", feedbackReq.PRNumber, feedbackReq.RepoFullName))

	result, err := s.engine.Feedback(ctx, feedbackReq)
	if err != nil {
		s.jobs.AddLog(job.ID, fmt.Sprintf("feedback failed: %v", err))
		return nil, err
	}

	s.jobs.AddLog(job.ID, fmt.Sprintf("feedback completed: status=%s, files_changed=%d", result.Status, len(result.FilesChanged)))
	return result, nil
}
