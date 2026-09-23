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

// problemError carries a problem document as a tool's error. A failed call is
// a successful MCP response with the result marked as an error, so there is no
// status line to put the problem on: the SDK puts the error's text in the
// result, and the text here is the document, the same JSON the API would send.
//
// Its being this type is also how argumentProblems tells a failure a tool
// reported from one the SDK reported before the tool ran.
type problemError struct {
	doc map[string]any
}

func (e *problemError) Error() string {
	b, err := json.Marshal(e.doc)
	if err != nil {
		// Every member is a string, a number, or a value a Problem's own
		// custom members hold, so this is not reached in practice.
		return http.StatusText(http.StatusInternalServerError)
	}

	return string(b)
}

// toolProblem describes an error a tool's handler returned the way the API
// would: an error problem.Resolve recognises becomes its problem, and anything
// else is logged here and answered with problem.Internal, so the cause -- a
// query that failed, say -- stays in the server log rather than reaching the
// agent.
func toolProblem(ctx context.Context, log *slog.Logger, tool string, err error) *problemError {
	p, ok := problem.Resolve(err)
	if !ok {
		log.ErrorContext(ctx, "MCP tool failed", slog.String("tool", tool), logger.Err(err))

		p = problem.Internal()
	}

	return &problemError{doc: problem.Document(p, tool)}
}

// argumentProblems is receiving middleware that makes the one kind of tool
// failure no handler sees a problem document too: arguments the SDK refused
// against the tool's input schema before calling it. Every error a handler
// returns is already a problemError by then, so any other error on a tool
// result can only be the SDK's account of what was wrong with the arguments.
// That account names the property and the values it admits, so it is kept as
// the detail.
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

		// SetError keeps whatever content a result already has, and the SDK
		// set the refusal's text there, so it is cleared for the document.
		result.Content = nil
		result.SetError(&problemError{doc: problem.Document(p, tool)})

		return result, nil
	}
}
