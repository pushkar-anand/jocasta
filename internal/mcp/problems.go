package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/problem"
)

// problemError holds a problem document as the error of a tool. A failed call
// is a successful MCP response, with the result marked as an error. So there
// is no status line for the problem. The SDK puts the text of the error in the
// result. Here, the text is the document, the same JSON that the API would
// send.
//
// This type also lets argumentProblems tell a failure from a tool apart from a
// failure that the SDK reported before the tool ran.
type problemError struct {
	doc map[string]any
}

func (e *problemError) Error() string {
	b, err := json.Marshal(e.doc)
	if err != nil {
		// Every member is a string, a number, or a value of the custom members
		// of a Problem. So this code does not run in practice.
		return http.StatusText(http.StatusInternalServerError)
	}

	return string(b)
}

// toolProblem describes an error from the handler of a tool, as the API would.
// An error that problem.Resolve knows becomes its problem. toolProblem logs any
// other error and answers with problem.Internal. So the cause, such as a
// failed query, stays in the server log.
func toolProblem(ctx context.Context, log *slog.Logger, tool string, err error) *problemError {
	p, ok := problem.Resolve(err)
	if !ok {
		log.ErrorContext(ctx, "MCP tool failed", slog.String("tool", tool), logger.Err(err))

		p = problem.Internal()
	}

	return &problemError{doc: problem.Document(p, tool)}
}

// argumentProblems is receiving middleware. It also makes a problem document
// of the one kind of tool failure that no handler sees. This is when the SDK
// refuses arguments against the input schema of the tool, before it calls the
// tool. By then, every error from a handler is already a problemError. So any
// other error on a tool result can only be the report of the SDK about the
// arguments. That report names the property and its allowed values. So
// argumentProblems keeps it as the detail.
func argumentProblems(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		res, err := next(ctx, method, req)
		if err != nil || method != "tools/call" {
			return res, err
		}

		result, ok := res.(*mcpsdk.CallToolResult)
		if !ok || !result.IsError {
			return res, nil
		}

		cause := result.GetError()
		if cause == nil {
			return res, nil
		}

		if _, ours := errors.AsType[*problemError](cause); ours {
			return res, nil
		}

		var tool string
		if call, ok := req.(*mcpsdk.CallToolRequest); ok && call.Params != nil {
			tool = call.Params.Name
		}

		p := response.NewProblem().
			WithStatus(http.StatusBadRequest).
			WithDetail(cause.Error()).
			Build()

		// SetError keeps the content that a result already has. The SDK put
		// the text of the refusal there. So clear it for the document.
		result.Content = nil
		result.SetError(&problemError{doc: problem.Document(p, tool)})

		return result, nil
	}
}
