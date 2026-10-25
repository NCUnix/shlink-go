package types

import (
	"bytes"
	"encoding/json"
	"time"
)

const dateLayout = "2006-01-02T15:04:05-07:00"

type Time struct {
	time.Time
}

func NewTime(t time.Time) Time {
	return Time{Time: t.UTC()}
}

func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + t.Format(dateLayout) + `"`), nil
}

func (t *Time) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		t.Time = time.Time{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return err
	}
	t.Time = parsed.UTC()
	return nil
}

func (t Time) String() string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateLayout)
}
