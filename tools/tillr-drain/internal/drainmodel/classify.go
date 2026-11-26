package drainmodel

import "github.com/mschulkind-oss/tillr/checkresult"

// ClassifyCheck routes every drain check-result observation through the shared
// classifier. Tests replace it via withClassifier to verify site routing.
var ClassifyCheck = checkresult.Classify
