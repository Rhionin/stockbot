package server

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/export"
	"github.com/google/uuid"
)

const exportTTL = 15 * time.Minute

type savedExport struct {
	id          string
	householdID string
	bundle      export.Bundle
	expires     time.Time
}

type exportJobs struct {
	mu   sync.Mutex
	jobs map[string]*savedExport
}

func (s *stage1) serveCreateExport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ownerHousehold(w, r, "")
	if !ok {
		return
	}
	bundle, ok := s.buildExport(w, r, p.UserID, p.HouseholdID)
	if !ok {
		return
	}
	job := s.saveExport(p.HouseholdID, bundle)
	writeJSON(w, http.StatusAccepted, struct {
		JobID  string `json:"jobId"`
		Status string `json:"status"`
	}{JobID: job.id, Status: "ready"})
}

func (s *stage1) serveExportStatus(w http.ResponseWriter, r *http.Request) {
	job, ok := s.exportForOwner(w, r)
	if !ok {
		return
	}
	expires := job.expires.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, struct {
		JobID     string `json:"jobId"`
		Status    string `json:"status"`
		JSONURL   string `json:"jsonUrl"`
		CSVURL    string `json:"csvUrl"`
		ExpiresAt string `json:"expiresAt"`
	}{
		JobID: job.id, Status: "ready",
		JSONURL:   "/api/export/" + job.id + "/stockbot-export.json",
		CSVURL:    "/api/export/" + job.id + "/items.csv",
		ExpiresAt: expires,
	})
}

func (s *stage1) serveExportZip(w http.ResponseWriter, r *http.Request) {
	job, ok := s.exportForOwner(w, r)
	if !ok {
		return
	}
	if !s.auditExport(w, r, job.householdID) {
		return
	}
	writeAttachment(w, "application/zip", "stockbot-export.zip", job.bundle.Zip)
}

func (s *stage1) serveExportJSON(w http.ResponseWriter, r *http.Request) {
	job, ok := s.exportForOwner(w, r)
	if !ok {
		return
	}
	if !s.auditExport(w, r, job.householdID) {
		return
	}
	writeAttachment(w, "application/json", "stockbot-export.json", job.bundle.JSON)
}

func (s *stage1) serveExportItems(w http.ResponseWriter, r *http.Request) {
	job, ok := s.exportForOwner(w, r)
	if !ok {
		return
	}
	if !s.auditExport(w, r, job.householdID) {
		return
	}
	writeAttachment(w, "text/csv; charset=utf-8", "items.csv", job.bundle.ItemsCSV)
}

func (s *stage1) serveExportShopping(w http.ResponseWriter, r *http.Request) {
	job, ok := s.exportForOwner(w, r)
	if !ok {
		return
	}
	if !s.auditExport(w, r, job.householdID) {
		return
	}
	writeAttachment(w, "text/csv; charset=utf-8", "shopping-list.csv", job.bundle.ShoppingCSV)
}

func (s *stage1) serveHouseholdExport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.ownerHousehold(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	bundle, ok := s.buildExport(w, r, p.UserID, p.HouseholdID)
	if !ok {
		return
	}
	if !s.auditExport(w, r, p.HouseholdID) {
		return
	}
	writeAttachment(w, "application/zip", "stockbot-export.zip", bundle.Zip)
}

func (s *stage1) ownerHousehold(w http.ResponseWriter, r *http.Request, householdID string) (auth.Principal, bool) {
	p, ok := s.account(w, r)
	if !ok {
		return auth.Principal{}, false
	}
	if householdID == "" {
		householdID = p.HouseholdID
	}
	if householdID == "" {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation, Field: householdHeader})
		return auth.Principal{}, false
	}
	if err := s.svc.ExportAllowed(r.Context(), p.UserID, householdID); err != nil {
		writeStage1(w, err)
		return auth.Principal{}, false
	}
	p.HouseholdID = householdID
	p.Role = "owner"
	return p, true
}

func (s *stage1) buildExport(w http.ResponseWriter, r *http.Request, userID, householdID string) (export.Bundle, bool) {
	bundle, err := export.Build(r.Context(), s.db(), householdID, s.svc.Now())
	if err != nil {
		if errors.Is(err, export.ErrNotFound) {
			writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
			return export.Bundle{}, false
		}
		writeStage1(w, err)
		return export.Bundle{}, false
	}
	_ = userID
	return bundle, true
}

func (s *stage1) auditExport(w http.ResponseWriter, r *http.Request, householdID string) bool {
	p, ok := auth.PrincipalFrom(r.Context())
	actor := ""
	if ok {
		actor = p.UserID
	}
	if err := s.svc.RecordExport(r.Context(), "account", actor, householdID); err != nil {
		writeStage1(w, err)
		return false
	}
	return true
}

func (s *stage1) exportForOwner(w http.ResponseWriter, r *http.Request) (*savedExport, bool) {
	p, ok := s.account(w, r)
	if !ok {
		return nil, false
	}
	job, ok := s.lookupExport(r.PathValue("id"))
	if !ok {
		writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
		return nil, false
	}
	if err := s.svc.ExportAllowed(r.Context(), p.UserID, job.householdID); err != nil {
		writeStage1(w, err)
		return nil, false
	}
	return job, true
}

func (s *stage1) saveExport(householdID string, bundle export.Bundle) *savedExport {
	job := &savedExport{
		id: uuid.NewString(), householdID: householdID, bundle: bundle,
		expires: s.svc.Now().Add(exportTTL),
	}
	s.exports.mu.Lock()
	defer s.exports.mu.Unlock()
	if s.exports.jobs == nil {
		s.exports.jobs = map[string]*savedExport{}
	}
	now := s.svc.Now()
	for id, saved := range s.exports.jobs {
		if !now.Before(saved.expires) {
			delete(s.exports.jobs, id)
		}
	}
	s.exports.jobs[job.id] = job
	return job
}

func (s *stage1) lookupExport(id string) (*savedExport, bool) {
	s.exports.mu.Lock()
	defer s.exports.mu.Unlock()
	job := s.exports.jobs[id]
	if job == nil || s.svc == nil || !s.svc.Now().Before(job.expires) {
		if job != nil {
			delete(s.exports.jobs, id)
		}
		return nil, false
	}
	return job, true
}

func writeAttachment(w http.ResponseWriter, contentType, filename string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
