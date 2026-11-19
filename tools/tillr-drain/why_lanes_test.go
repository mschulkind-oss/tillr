package main

import (
	"reflect"
	"testing"
)

// `tillr drain why --lanes 16` was read as one lane named "16" on 2026-11-19, and every
// schedulable item after the first was reported no-lane behind it. A count is the loop's
// --lanes spelling and offers the loop's lanes; a list is still a list.
func TestWhyLanesReadsACountAsTheLoopsLanes(t *testing.T) {
	got := *whyLanes("3", 2)
	want := []string{"lane-1", "lane-2", "lane-3", "express-1", "express-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a count offered %v, want %v", got, want)
	}
	got = *whyLanes("lane-2,express-1", 2)
	if want := []string{"lane-2", "express-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a list offered %v, want %v", got, want)
	}
}
