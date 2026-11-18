package item

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
