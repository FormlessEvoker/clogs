CREATE TABLE http_details (
 event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 client_address TEXT, server_port INTEGER, method TEXT, raw_target TEXT, path TEXT,
 raw_query TEXT, route_template TEXT, site TEXT, http_version TEXT, status INTEGER,
 response_bytes INTEGER
);
CREATE INDEX http_status_idx ON http_details(status);
CREATE INDEX http_route_idx ON http_details(route_template);
CREATE INDEX http_site_idx ON http_details(site);
