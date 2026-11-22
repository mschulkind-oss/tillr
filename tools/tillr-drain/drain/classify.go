package drain

import "github.com/mschulkind-oss/tillr/checkresult"

// classifyCheck routes every drain check-result observation through the shared
// classifier. Tests replace it via withClassifier to verify site routing.
var classifyCheck = checkresult.Classify
