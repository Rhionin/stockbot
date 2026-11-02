package server

import "net/http"

func (s *stage1) serveGetKrogerApp(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.account(w, r); !ok {
		return
	}
	if s.krogerApp == nil {
		writeStage1(w, &HTTPError{Code: http.StatusNotFound, Message: "not found", ErrorCode: codeNotFound})
		return
	}
	HandleJSON(s.krogerApp.Get).ServeHTTP(w, r)
}

func (s *stage1) servePutKrogerApp(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.account(w, r); !ok {
		return
	}
	if s.krogerApp == nil {
		writeStage1(w, &HTTPError{Code: http.StatusNotFound, Message: "not found", ErrorCode: codeNotFound})
		return
	}
	HandleJSON(s.krogerApp.Put).ServeHTTP(w, r)
}

func (s *stage1) serveDeleteKrogerApp(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.account(w, r); !ok {
		return
	}
	if s.krogerApp == nil {
		writeStage1(w, &HTTPError{Code: http.StatusNotFound, Message: "not found", ErrorCode: codeNotFound})
		return
	}
	HandleJSON(s.krogerApp.Delete).ServeHTTP(w, r)
}

func (s *stage1) serveTestKrogerApp(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.account(w, r); !ok {
		return
	}
	if s.krogerApp == nil {
		writeStage1(w, &HTTPError{Code: http.StatusNotFound, Message: "not found", ErrorCode: codeNotFound})
		return
	}
	HandleJSON(s.krogerApp.Test).ServeHTTP(w, r)
}
