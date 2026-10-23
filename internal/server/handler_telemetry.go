package server

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Rhionin/stockbot/internal/telemetry"
	"github.com/go-json-experiment/json"
)

// maxClientReportBytes bounds a browser report. The fields are short on
// purpose; anything larger is rejected rather than stored.
const maxClientReportBytes = 4096

// TelemetryHandler serves the in-process snapshot and accepts browser reports.
type TelemetryHandler struct {
	Registry *telemetry.Registry
}

// Get implements GET /api/telemetry.
func (h *TelemetryHandler) Get(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.Registry.Snapshot())
}

// PostClient implements POST /api/telemetry/client.
func (h *TelemetryHandler) PostClient(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxClientReportBytes)
	var in telemetry.InboundReport
	if err := json.UnmarshalRead(r.Body, &in); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, codeValidation, "invalid report")
			return
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "too large") {
			writeError(w, http.StatusBadRequest, codePayloadTooLarge, "report is too large")
			return
		}
		writeError(w, http.StatusBadRequest, codeValidation, "invalid report")
		return
	}
	if err := h.Registry.AcceptClientReport(in); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
