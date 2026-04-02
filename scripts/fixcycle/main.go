package main

import (
	"fmt"
	"os"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
)

func main() {
	database, err := db.Open("/workspace/tillr.db")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Find the active cycle for human-workstreams
	cycle, err := db.GetActiveCycle(database, "human-workstreams")
	if err != nil {
		fmt.Fprintf(os.Stderr, "get cycle: %v\n", err)
		os.Exit(1)
	}
	if cycle == nil {
		fmt.Println("no active cycle found")
		os.Exit(1)
	}

	fmt.Printf("found active cycle %d (type=%s step=%d)\n", cycle.ID, cycle.CycleType, cycle.CurrentStep)

	// Score the design step (step 3)
	_, err = database.Exec(
		`INSERT INTO cycle_scores (cycle_id, step, iteration, score, notes) VALUES (?, ?, ?, ?, ?)`,
		cycle.ID, 3, cycle.Iteration, 9.0,
		"Design phase complete: comprehensive implementation plan at docs/design/workstreams-implementation-plan.md covering 7 areas (markdown rendering, cycle approve/reject UI, agent context, note improvements, bidirectional display, JIT cycle steps, dashboard widget). Name candidates doc regenerated with availability data at docs/NAME_CANDIDATES.md.",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "score: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("scored design step (9.0)")

	// Advance to step 4 (human-approve)
	err = db.UpdateCycleInstance(database, cycle.ID, 4, cycle.Iteration, "active")
	if err != nil {
		fmt.Fprintf(os.Stderr, "advance: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("advanced to step 4 (human-approve)")

	// Log event
	p, _ := db.GetProject(database)
	projectID := ""
	if p != nil {
		projectID = p.ID
	}
	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: "human-workstreams",
		EventType: "cycle.advanced",
		Data:      `{"from":"design","to":"human-approve"}`,
	})
	fmt.Println("logged cycle.advanced event")
}
