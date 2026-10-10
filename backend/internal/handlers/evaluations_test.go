package handlers_test

import (
	"encoding/json"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// pinRawMessage only accepts exactly json.RawMessage. A plain
// `var _ json.RawMessage = x` is NOT enough: []byte is assignable to
// json.RawMessage, so it would also compile against the base64-marshalling
// []byte the nullable-JSONB trap generates.
func pinRawMessage[T json.RawMessage](T) {}

// H4: nullable JSONB columns must generate json.RawMessage. Compile-time pin
// of the sqlc nullable override.
func init() {
	pinRawMessage(db.Evaluation{}.Scores)
	pinRawMessage(db.Survey{}.Questions)
	pinRawMessage(db.SurveyResponse{}.Answers)
}
