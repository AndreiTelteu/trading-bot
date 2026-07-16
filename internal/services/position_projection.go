package services

import (
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm"
)

// updatePositionOperational writes only non-economic mark/exit-control columns.
// The timestamp predicate prevents an older worker observation from replacing a
// newer mark; status=open prevents a stale worker from touching a closed row.
func updatePositionOperational(positionID uint, observedAt time.Time, values map[string]interface{}) (bool, error) {
	values["last_mark_at"] = observedAt
	if mark, ok := values["current_price"].(float64); ok && mark > 0 {
		values["pnl"] = gorm.Expr("(? - avg_price) * amount", mark)
		values["pnl_percent"] = gorm.Expr("CASE WHEN avg_price > 0 THEN ((? - avg_price) / avg_price) * 100 ELSE 0 END", mark)
	}
	result := database.DB.Model(&database.Position{}).
		Where("id = ? AND status = ? AND (last_mark_at IS NULL OR last_mark_at <= ?)", positionID, "open", observedAt).
		Updates(values)
	return result.RowsAffected == 1, result.Error
}
