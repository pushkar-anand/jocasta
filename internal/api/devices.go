package api

import (
	"net/http"
	"strconv"

	"github.com/go-playground/validator/v10"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/inventoryapi"
)

// DeviceClassRule validates the closed set of device classes shared by both APIs.
func DeviceClassRule(fl validator.FieldLevel) bool {
	return classify.Class(fl.Field().String()).Valid()
}

func (h *Handler) updateDevice(operations *inventoryapi.Service) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := deviceID(r)
		if err != nil {
			return err
		}

		body, err := h.reader.ReadAndValidateJSON[inventoryapi.Curation](r)
		if err != nil {
			return err
		}

		device, err := operations.UpdateDeviceCuration(r.Context(), inventoryapi.Update{
			DeviceID: inventoryapi.DeviceID{ID: id}, Curation: *body,
		})
		if err != nil {
			return operationError(err)
		}

		h.jsonWriter.Ok(w, r, device)

		return nil
	}
}

func deviceID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, badRequest("ID must be a positive whole number.")
	}

	return id, nil
}
