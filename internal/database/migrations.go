package database

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

func schemaModels() []interface{} {
	return []interface{}{
		&Wallet{},
		&Position{},
		&Order{},
		&Setting{},
		&AIProposal{},
		&IndicatorWeight{},
		&LLMConfig{},
		&ActivityLog{},
		&BacktestJob{},
		&TrendAnalysisHistory{},
		&PortfolioSnapshot{},
	}
}

func migrateSchema(db *gorm.DB) error {
	return db.AutoMigrate(schemaModels()...)
}

func RunMigrations(db *gorm.DB) error {
	m := gormigrate.New(db, &gormigrate.Options{
		TableName:      "schema_migrations",
		UseTransaction: true,
	}, []*gormigrate.Migration{
		{
			ID: "202603221700_initial_postgres_schema",
			Migrate: func(tx *gorm.DB) error {
				return migrateSchema(tx)
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable(schemaModels()...)
			},
		},
		{
			ID: "202603221830_backtest_job_summary_compact_json",
			Migrate: func(tx *gorm.DB) error {
				if tx.Migrator().HasColumn(&BacktestJob{}, "SummaryCompactJSON") {
					return nil
				}
				return tx.Migrator().AddColumn(&BacktestJob{}, "SummaryCompactJSON")
			},
			Rollback: func(tx *gorm.DB) error {
				if !tx.Migrator().HasColumn(&BacktestJob{}, "SummaryCompactJSON") {
					return nil
				}
				return tx.Migrator().DropColumn(&BacktestJob{}, "SummaryCompactJSON")
			},
		},
	})

	return m.Migrate()
}
