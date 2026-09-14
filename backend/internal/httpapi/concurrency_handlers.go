package httpapi

import (
	"github.com/sharesub/sharesub/backend/internal/domain"
	"net/http"
)

func (s *Server) planConcurrency(w http.ResponseWriter, r *http.Request) {
	v, err := s.app.PlanConcurrency(r.Context(), currentUser(r).ID, r.PathValue("planID"))
	writeResult(w, v, err)
}
func (s *Server) updatePlanConcurrency(w http.ResponseWriter, r *http.Request) {
	var input domain.ConcurrencyPolicy
	if !decodeJSON(w, r, &input) {
		return
	}
	err := s.app.UpdatePlanConcurrency(r.Context(), currentUser(r).ID, r.PathValue("planID"), input)
	writeResult(w, map[string]bool{"updated": err == nil}, err)
}
func (s *Server) adminPlanConcurrency(w http.ResponseWriter, r *http.Request) {
	v, err := s.app.AdminPlanConcurrency(r.Context(), currentUser(r), r.PathValue("planID"))
	writeResult(w, v, err)
}
func (s *Server) adminUpdatePlanConcurrency(w http.ResponseWriter, r *http.Request) {
	var input domain.ConcurrencyPolicy
	if !decodeJSON(w, r, &input) {
		return
	}
	err := s.app.AdminUpdatePlanConcurrency(r.Context(), currentUser(r), r.PathValue("planID"), input)
	writeResult(w, map[string]bool{"updated": err == nil}, err)
}
