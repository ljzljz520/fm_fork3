// Package preview defines the cross-format request identity and size
// versioning used by the right hand preview pane.
//
// Every explicit preview (regardless of its format: code, csv, image,
// markdown or pdf) is tagged with a RequestID taken from one counter
// owned by the tui. Results produced by asynchronous commands carry the
// request and, for renderers whose output depends on the pane width,
// the size generation they were rendered for. A result is only
// committed when both generations still match the current ones, so
// out of order completions, cross format switches and resets can never
// let stale content, errors or timeout messages overwrite the latest
// request.
package preview

import "context"

// RequestID identifies one explicit preview request. It is shared by
// every preview format, so a request that switched formats invalidates
// the in flight work of the previous format as well.
type RequestID uint64

// SizeVersion identifies a window size generation. Width sensitive
// renderers tag their results with the size they were rendered for and
// reject results rendered for an older size.
type SizeVersion uint64

// Counter hands out monotonically increasing preview generations. The
// zero value is ready to use.
type Counter struct {
	request RequestID
	size    SizeVersion
}

// NextRequest advances and returns the current request id.
func (c *Counter) NextRequest() RequestID {
	c.request++

	return c.request
}

// Request returns the current request id without advancing it.
func (c *Counter) Request() RequestID {
	return c.request
}

// NextSize advances and returns the current size version.
func (c *Counter) NextSize() SizeVersion {
	c.size++

	return c.size
}

// Size returns the current size version without advancing it.
func (c *Counter) Size() SizeVersion {
	return c.size
}

// Aborted reports whether ctx has been cancelled. Preview commands call
// it at their interruptible checkpoints (before and after blocking IO
// or rendering) and return no message when the work they represent has
// been superseded.
func Aborted(ctx context.Context) bool {
	if ctx == nil {
		return false
	}

	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
