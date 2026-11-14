package database

import "context"

// DeleteRoutineIDs deletes a confirmed snapshot rather than unrelated/new entries.
func (db *DB) DeleteRoutineIDs(ctx context.Context, ids []int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM rotinas_proativas WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
