package item

import "regexp"

var workflowRefShape = regexp.MustCompile(`^[a-z][a-z0-9-]*@v[1-9][0-9]*$`)

// WorkflowRefValid reports whether a reference names a versioned workflow file.
func WorkflowRefValid(ref string) bool { return workflowRefShape.MatchString(ref) }
