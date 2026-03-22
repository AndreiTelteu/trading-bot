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
	})

	return m.Migrate()
}
