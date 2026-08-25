package report

import (
	"context"
	"database/sql"
)

type Count struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}
type Stats struct {
	Source            string  `json:"source,omitempty"`
	TimeStart         *string `json:"time_start,omitempty"`
	TimeEnd           *string `json:"time_end,omitempty"`
	SourceFiles       int64   `json:"source_files"`
	ParseWarnings     int64   `json:"parse_warnings"`
	Families          []Count `json:"families"`
	Severities        []Count `json:"severities"`
	HTTPStatusClasses []Count `json:"http_status_classes"`
	Routes            []Count `json:"routes"`
	Signatures        []Count `json:"signatures"`
	Exceptions        []Count `json:"exceptions"`
	Protocols         []Count `json:"protocols"`
	Sources           []Count `json:"sources"`
}

func CollectStats(ctx context.Context, db *sql.DB, source string) (Stats, error) {
	stats := Stats{Source: source}
	var start, end sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT MIN(e.occurred_at_utc),MAX(e.occurred_at_utc) FROM events e JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?)`, source, source).Scan(&start, &end); err != nil {
		return stats, err
	}
	if start.Valid {
		stats.TimeStart = &start.String
	}
	if end.Valid {
		stats.TimeEnd = &end.String
	}
	var err error
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_files WHERE (?='' OR source_label=?)`, source, source).Scan(&stats.SourceFiles); err != nil {
		return stats, err
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events e JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?) AND e.parse_warnings IS NOT NULL`, source, source).Scan(&stats.ParseWarnings); err != nil {
		return stats, err
	}
	if stats.Families, err = counts(ctx, db, `SELECT e.family,COUNT(*) FROM events e JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?) GROUP BY e.family ORDER BY COUNT(*) DESC,e.family`, source); err != nil {
		return stats, err
	}
	if stats.Severities, err = counts(ctx, db, `SELECT COALESCE(e.severity,'(none)'),COUNT(*) FROM events e JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?) GROUP BY e.severity ORDER BY COUNT(*) DESC,COALESCE(e.severity,'')`, source); err != nil {
		return stats, err
	}
	if stats.HTTPStatusClasses, err = counts(ctx, db, `SELECT CAST(h.status/100 AS TEXT)||'xx',COUNT(*) FROM http_details h JOIN events e ON e.id=h.event_id JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?) GROUP BY h.status/100 ORDER BY h.status/100`, source); err != nil {
		return stats, err
	}
	if stats.Routes, err = counts(ctx, db, `SELECT h.route_template,COUNT(*) FROM http_details h JOIN events e ON e.id=h.event_id JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?) GROUP BY h.route_template ORDER BY COUNT(*) DESC,h.route_template LIMIT 10`, source); err != nil {
		return stats, err
	}
	if stats.Signatures, err = counts(ctx, db, `SELECT s.fingerprint,COUNT(*) FROM signatures s JOIN events e ON e.signature_id=s.id JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?) GROUP BY s.fingerprint ORDER BY COUNT(*) DESC,s.fingerprint LIMIT 10`, source); err != nil {
		return stats, err
	}
	if stats.Exceptions, err = counts(ctx, db, `SELECT j.exception_class,COUNT(*) FROM java_details j JOIN events e ON e.id=j.event_id JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?) AND j.exception_class IS NOT NULL AND j.exception_class<>'' GROUP BY j.exception_class ORDER BY COUNT(*) DESC,j.exception_class LIMIT 10`, source); err != nil {
		return stats, err
	}
	if stats.Protocols, err = counts(ctx, db, `SELECT j.protocol_type,COUNT(*) FROM java_details j JOIN events e ON e.id=j.event_id JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?) AND j.protocol_type IS NOT NULL AND j.protocol_type<>'' GROUP BY j.protocol_type ORDER BY COUNT(*) DESC,j.protocol_type LIMIT 10`, source); err != nil {
		return stats, err
	}
	if stats.Sources, err = counts(ctx, db, `SELECT sf.source_label,COUNT(*) FROM events e JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?) GROUP BY sf.source_label ORDER BY COUNT(*) DESC,sf.source_label`, source); err != nil {
		return stats, err
	}
	return stats, nil
}
func counts(ctx context.Context, db *sql.DB, query, source string) ([]Count, error) {
	rows, err := db.QueryContext(ctx, query, source, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []Count
	for rows.Next() {
		var value Count
		if err := rows.Scan(&value.Name, &value.Count); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
