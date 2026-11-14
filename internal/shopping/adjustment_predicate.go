package shopping

// ActiveAdjustmentSQL filters adjustment rows that reads should treat as present.
// Non-zero quantities are always returned; a zero is active when expires_trip is
// null (legacy or lasting non-zero semantics do not apply to qty 0) or when
// expires_trip is still after the household trip_seq.
const ActiveAdjustmentSQL = `(a.quantity != 0 OR a.expires_trip IS NULL OR a.expires_trip > h.trip_seq)`

// LapsedZeroAdjustmentSQL matches zero rows deleted at trip advance (AC6 legacy NULL included).
const LapsedZeroAdjustmentSQL = `quantity = 0 AND (expires_trip IS NULL OR expires_trip <= (SELECT trip_seq FROM households WHERE id = ?))`