package window

// Mode tells the caller how the application should decide when to stop.
type Mode int

const (
	// Closed means Open displayed the window itself and has returned because
	// that window was closed. The application should shut down.
	Closed Mode = iota
	// Handed means the interface was handed to a browser whose lifetime is not
	// ours to observe: it may be a browser the operator already had running, so
	// it neither starts nor exits with us. The application should keep serving
	// and stop when the page itself stops reporting in.
	Handed
	// Detached means no window could be opened. The application should keep
	// serving until it is interrupted.
	Detached
)
