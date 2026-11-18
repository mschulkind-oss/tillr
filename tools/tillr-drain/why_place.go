package main

import "strconv"

func whyPlace(place int) string {
	if place == 0 {
		return "—"
	}
	return strconv.Itoa(place)
}
