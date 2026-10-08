package app

import "errors"

// Expiry closes the original workflow. A new explicit user request starts
// another workflow; renewing a stale card must not revive this run.
func (s *Store) RenewExpiredApproval(id string) (Question, error) {
	q, err := s.GetQuestion(id)
	if err != nil {
		return q, err
	}
	return q, errors.New("approval expired; this workflow is concluding; start a new request to authorize new work")
}
