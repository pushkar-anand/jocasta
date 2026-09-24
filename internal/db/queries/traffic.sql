-- name: UpsertTraffic :exec
-- One flush adds to the hour's running totals. The peer's identity columns
-- take the newest non-empty answer: a name that failed to resolve on one
-- flush should not erase one that resolved on the last.
INSERT INTO traffic_hourly (source_id, device_id, hour, peer_device_id, peer_ip, peer_name, peer_asn,
                            protocol, service_port, bytes_out, bytes_in, packets_out, packets_in, connections)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (device_id, hour, source_id, peer_ip, protocol, service_port) DO UPDATE
    SET peer_device_id = COALESCE(excluded.peer_device_id, peer_device_id),
        peer_name      = COALESCE(excluded.peer_name, peer_name),
        peer_asn       = COALESCE(excluded.peer_asn, peer_asn),
        bytes_out      = bytes_out + excluded.bytes_out,
        bytes_in       = bytes_in + excluded.bytes_in,
        packets_out    = packets_out + excluded.packets_out,
        packets_in     = packets_in + excluded.packets_in,
        connections    = connections + excluded.connections;

-- name: DeleteTrafficBefore :execrows
DELETE
FROM traffic_hourly
WHERE hour < ?;
