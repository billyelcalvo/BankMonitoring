package valueobjects

// FailureReason explains why a transfer ended up as failed. Failed transfers
// are still stored so a retry with the same key gets the same answer.
type FailureReason string

const (
	FailureReasonNone              FailureReason = ""
	FailureReasonInsufficientFunds FailureReason = "insufficient_funds"
)
