// Package model defines normalized log events shared by parsers and later
// storage/export layers.
package model

import "time"

type Family string

const (
	FamilyJVMMultiline Family = "jvm-multiline"
	FamilyAccess       Family = "access"
	FamilyCatalina     Family = "catalina"
)

type Precision string

const (
	PrecisionSecond      Precision = "second"
	PrecisionMillisecond Precision = "millisecond"
)

type Event struct {
	SourceLabel       string       `json:"source_label,omitempty"`
	SourcePath        string       `json:"source_path"`
	Family            Family       `json:"family"`
	OccurredAt        time.Time    `json:"occurred_at"`
	OriginalTimestamp string       `json:"original_timestamp"`
	Precision         Precision    `json:"precision"`
	SourceLineStart   int64        `json:"source_line_start"`
	SourceLineEnd     int64        `json:"source_line_end"`
	Severity          string       `json:"severity,omitempty"`
	Message           string       `json:"message"`
	RawText           string       `json:"raw_text"`
	ParseWarnings     []string     `json:"parse_warnings,omitempty"`
	Signature         string       `json:"signature"`
	Java              *JavaDetails `json:"java,omitempty"`
	HTTP              *HTTPDetails `json:"http,omitempty"`
}

type HTTPDetails struct {
	ClientAddress string `json:"client_address,omitempty"`
	ServerPort    int    `json:"server_port,omitempty"`
	Method        string `json:"method,omitempty"`
	RawTarget     string `json:"raw_target,omitempty"`
	Path          string `json:"path,omitempty"`
	RawQuery      string `json:"raw_query,omitempty"`
	RouteTemplate string `json:"route_template,omitempty"`
	Site          string `json:"site,omitempty"`
	HTTPVersion   string `json:"http_version,omitempty"`
	Status        int    `json:"status,omitempty"`
	ResponseBytes *int64 `json:"response_bytes,omitempty"`
}

type JavaDetails struct {
	Logger           string `json:"logger,omitempty"`
	Operation        string `json:"operation,omitempty"`
	Thread           string `json:"thread,omitempty"`
	ProtocolType     string `json:"protocol_type,omitempty"`
	ExceptionClass   string `json:"exception_class,omitempty"`
	ExceptionMessage string `json:"exception_message,omitempty"`
	RootCauseClass   string `json:"root_cause_class,omitempty"`
	RootCauseMessage string `json:"root_cause_message,omitempty"`
	StackTrace       string `json:"stack_trace,omitempty"`
}
