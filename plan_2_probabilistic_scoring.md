Implementation Plan 2: Probabilistic Signal Scoring

Goal
Replace discrete rating thresholds with a calibrated probability model to reduce false positives and improve risk-adjusted returns.

Order of Execution
1) Feature definition and extraction
2) Offline model training pipeline
3) Model storage and configuration
4) Online inference integration
5) Backtesting and benchmark comparison
6) Validation, monitoring, and retraining schedule

Scope
- Backend: services/indicators.go, services/trending.go, services/analyzer.go
- Storage: settings table for model coefficients and thresholds
- Offline tooling: external training script using historical OHLCV
- Frontend: settings UI for model parameters

Technical Design

1) Feature definition
Compute continuous features per 15m bar:
- RSI value
- MACD histogram
- Bollinger %B: (price - lower) / (upper - lower)
- Momentum percent
- Volume ratio: current volume / volume MA
- Optional: volatility ratio (ATR / price)

2) Labeling
Define future return label for horizon H bars:
- future_return = (close[t+H] - close[t]) / close[t]
- y = 1 if future_return > 0, else 0

3) Model training
- Logistic regression or gradient-boosted tree for probability p(up)
- Train on rolling 12-month windows, validate on next 3 months
- Standardize features and store coefficients for logistic regression

4) Inference integration
- Add a new module or helper:
  - computeProbUp(features, coeffs) -> p_up
  - computeEV(p_up, avg_gain, avg_loss) -> EV
- Buy gate: EV > EV_min and p_up > p_min

Pseudocode
features = extractFeatures(candles15m, currentPrice)
p_up = sigmoid(beta0 + beta1*RSI + beta2*MACD_hist + beta3*BB_pctB + beta4*momentum + beta5*vol_ratio)
ev = p_up * avg_gain - (1 - p_up) * avg_loss
allow_entry = (ev > EV_min) and (p_up > p_min)

5) Settings and storage
Add settings keys:
- prob_model_beta0..betaN
- prob_ev_min
- prob_p_min
- prob_avg_gain
- prob_avg_loss
- prob_model_enabled

6) Integration into trade decision
- In AnalyzeTrendingCoins:
  - Compute p_up and EV from features
  - Replace or augment signalQualifies and confidenceQualifies
  - Example: require current signal == BUY and prob gate true

Function-Level Changes

services/indicators.go
- Add CalculateBBPercentB(bb, price) float64
- Add CalculateVolumeRatio(volumes, ma) float64
- Add CalculateFeatureVector(candles []Candle, config IndicatorConfig) FeatureVector

services/trending.go
- Add computeProbGate(features FeatureVector, settings map[string]string) (pUp, ev float64, ok bool)
- Apply prob gate in AnalyzeTrendingCoins

database/database.go
- Add default settings for probabilistic model coefficients and thresholds

frontend/src/components/SettingsPanel.jsx
- Add inputs for probability model parameters

Backtesting Methodology
- Use 24+ months OHLCV and a fixed horizon H (e.g., 6–12 bars)
- Compare baseline vs probabilistic gating
- Include fees, slippage, and position caps
- Output metrics: Sharpe, max drawdown, win rate, profit factor

Validation
- Walk-forward analysis with rolling windows
- Out-of-sample final 3–6 months
- Statistical tests: bootstrap 95% CI for Sharpe and profit factor
- Reject model updates if CI overlaps baseline

Monitoring and Retraining
- Track live calibration drift (expected vs actual hit rate)
- Retrain monthly or quarterly depending on regime changes

Milestones
1) Implement feature extraction and storage
2) Build offline training pipeline
3) Integrate inference and gating in code
4) Backtest and tune thresholds
5) Release behind prob_model_enabled flag
