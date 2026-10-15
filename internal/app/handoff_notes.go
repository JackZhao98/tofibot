package app

// maxHandoffNoteRunes bounds the private notes a colleague receives.
const maxHandoffNoteRunes = 2000

func (s *Store) setHandoffNotes(runID, notes string) error {
	_, err := s.db.Exec(`UPDATE runs SET handoff_notes=? WHERE id=?`, trimRunes(notes, maxHandoffNoteRunes), runID)
	return err
}

func (s *Store) handoffNotes(runID string) string {
	var notes string
	_ = s.db.QueryRow(`SELECT handoff_notes FROM runs WHERE id=?`, runID).Scan(&notes)
	return notes
}
