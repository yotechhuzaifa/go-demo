package httpapi

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// validate is a reusable validator instance. The go-playground validator caches
// struct metadata internally, so constructing one per request would waste CPU.
var validate = newValidator()

// newValidator configures the validator to report JSON field names instead of
// Go field names. Clients should see `due_date`, not `DueDate`.
func newValidator() *validator.Validate {
	instance := validator.New()

	instance.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return field.Name
		}
		return name
	})

	return instance
}

// validateDTO runs declarative validation tags on a request DTO.
//
// The returned error is intentionally human-readable and shaped like the domain
// validation errors: `field: rule description`. The HTTP handler maps it to the
// stable `validation_error` code.
func validateDTO(value any) error {
	err := validate.Struct(value)
	if err == nil {
		return nil
	}

	var validationErrors validator.ValidationErrors
	if !errors.As(err, &validationErrors) {
		return err
	}

	// Report the first problem. A complete field-level list would be a larger
	// API change; one clear message is easier to act on and matches the
	// existing domain error shape.
	first := validationErrors[0]
	return fmt.Errorf("%s: %s", first.Field(), validationMessage(first))
}

// validationMessage translates validator tags into operator-friendly text.
func validationMessage(fieldError validator.FieldError) string {
	switch fieldError.Tag() {
	case "required":
		return "is required"
	case "min":
		return fmt.Sprintf("must be at least %s characters", fieldError.Param())
	case "max":
		return fmt.Sprintf("must be at most %s characters", fieldError.Param())
	case "oneof":
		return fmt.Sprintf("must be one of %s", fieldError.Param())
	case "uuid":
		return "must be a UUID"
	case "dive":
		return "contains an invalid item"
	default:
		return fmt.Sprintf("failed validation %q", fieldError.Tag())
	}
}
