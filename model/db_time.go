package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
)

func getDBTimestamp(ctx context.Context) (int64, error) {
	if DB == nil {
		return 0, errors.New("database is not configured")
	}
	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}
	var ts int64
	var err error
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		err = db.Raw("SELECT EXTRACT(EPOCH FROM NOW())::bigint").Scan(&ts).Error
	case common.UsingMainDatabase(common.DatabaseTypeSQLite):
		err = db.Raw("SELECT strftime('%s','now')").Scan(&ts).Error
	default:
		err = db.Raw("SELECT UNIX_TIMESTAMP()").Scan(&ts).Error
	}
	if err != nil {
		return 0, err
	}
	if ts <= 0 {
		return 0, errors.New("database returned an invalid timestamp")
	}
	return ts, nil
}

// GetDBTimestamp returns a UNIX timestamp from database time.
// Falls back to application time on error.
func GetDBTimestamp() int64 {
	ts, err := getDBTimestamp(context.Background())
	if err != nil {
		return common.GetTimestamp()
	}
	return ts
}
