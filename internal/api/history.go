package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventoryapi"
)

// query keeps HTTP decoding and rendering separate from inventory operations.
func (h *Handler) query[In, Out any](call func(context.Context, In) (Out, error)) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		input, err := h.reader.ReadAndValidateQueryParams[In](r)
		if err != nil {
			return err
		}

		if target, ok := any(input).(interface{ SetID(int64) }); ok {
			id, err := deviceID(r)
			if err != nil {
				return err
			}

			target.SetID(id)
		}

		output, err := call(r.Context(), *input)
		if err != nil {
			return operationError(err)
		}

		h.jsonWriter.Ok(w, r, output)

		return nil
	}
}

func operationError(err error) error {
	var input *inventoryapi.InputError
	if errors.As(err, &input) {
		return badRequest(input.Detail)
	}

	return err
}
