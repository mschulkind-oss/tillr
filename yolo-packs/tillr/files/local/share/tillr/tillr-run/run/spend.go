package run

// DescribeSpend makes the absence of a backend measurement explicit without
// inventing a number. Partial measurements retain their individual nil fields.
func DescribeSpend(rec *Record) {
	if rec.Usage != nil {
		rec.SpendStatus = "reported"
		rec.SpendNote = ""
		return
	}
	rec.SpendStatus = "unreported"
	if rec.SpendNote != "" {
		return
	}
	if rec.EndCause == EndLeaseReclaimed {
		rec.SpendNote = "the run face left no terminal record; its lease was reclaimed"
	} else {
		rec.SpendNote = "the backend reported no spend measurement"
	}
}
