package engine

// Attention is what the app's Overview lists under "Needs attention": folders macOS hasn't let LocalSites
// read yet, failed Doctor checks (from the last time Doctor ran) and running projects with a problem.
type Attention struct {
	WaitingForAccess int `json:"waiting_for_access"`
	DoctorFails      int `json:"doctor_fails"`
	SiteProblems     int `json:"site_problems"`
	Total            int `json:"total"`
}

// NoteDoctor remembers how many checks failed, so the count can be shown without running Doctor again.
// The folder access check is left out because waiting folders are counted on their own.
func (e *Engine) NoteDoctor(checks []Check) {
	n := 0
	for _, c := range checks {
		if c.Status == "fail" && c.Name != "Folder access" {
			n++
		}
	}
	if e.doctorFails.Swap(int64(n)) != int64(n) {
		e.Changes.Bump()
	}
}

// Attention counts what needs the person right now.
func (e *Engine) Attention() Attention {
	a := Attention{WaitingForAccess: len(WaitingForAccess()), DoctorFails: int(e.doctorFails.Load())}
	for _, s := range e.Sites() {
		if s.Enabled && len(s.Problems) > 0 {
			a.SiteProblems++
		}
	}
	a.Total = a.WaitingForAccess + a.DoctorFails + a.SiteProblems
	return a
}
