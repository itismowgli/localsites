package main

// attention counts what needs the person, for the badge on the Dock icon: agent requests waiting for
// approval, problems they haven't dismissed, and an update that is ready or waiting to be installed.
func attention(approvals, problems int, upd updState) int {
	n := approvals + problems
	if upd.Latest != nil && !upd.Skipped && (upd.Phase == "available" || upd.Phase == "ready") {
		n++
	}
	return n
}
