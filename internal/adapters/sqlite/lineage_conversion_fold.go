package sqlite

import (
	"context"
	"fmt"
	"time"
)

// ConversionFoldSince is when format conversions started to be folded into
// the version they converted (#330): the moment migration 0068 first ran.
// Conversions made earlier keep their own version (lineage.Config).
func (r *Repository) ConversionFoldSince(ctx context.Context) (time.Time, error) {
	var raw string
	if err := r.db.QueryRowContext(ctx, `SELECT since FROM lineage_conversion_fold WHERE id = 1`).Scan(&raw); err != nil {
		return time.Time{}, fmt.Errorf("sqlite: read conversion fold start: %w", err)
	}
	since, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("sqlite: conversion fold start %q: %w", raw, err)
	}
	return since, nil
}
