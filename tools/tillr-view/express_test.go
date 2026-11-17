package main

import (
	"fmt"
	"testing"
)

func TestExpressLaneCapacityExcludesDesks(t *testing.T) {
	var rows []laneRow
	for i := 1; i <= 10; i++ {
		state := "idle"
		if i <= 7 {
			state = "running"
		}
		rows = append(rows, laneRow{LaneID: fmt.Sprintf("lane-%d", i), State: state})
	}
	rows = append(rows, laneRow{LaneID: "express-1", State: "running"}, laneRow{LaneID: "express-2", State: "idle"}, laneRow{LaneID: "desk-doc", State: "running"})
	if got := laneCapacityLine(rows); got != "7 of 10 running, 1 of 2 express" {
		t.Fatal(got)
	}
}
