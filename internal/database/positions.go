package database

const ClosedPositionsHistoryLimit = 50

func ListPositionsForDisplay() ([]Position, error) {
	var openPositions []Position
	if err := DB.Where("status = ?", "open").Order("opened_at DESC").Find(&openPositions).Error; err != nil {
		return nil, err
	}

	var closedPositions []Position
	if err := DB.Where("status = ?", "closed").Order("closed_at DESC").Order("opened_at DESC").Limit(ClosedPositionsHistoryLimit).Find(&closedPositions).Error; err != nil {
		return nil, err
	}

	positions := make([]Position, 0, len(openPositions)+len(closedPositions))
	positions = append(positions, openPositions...)
	positions = append(positions, closedPositions...)

	return positions, nil
}
