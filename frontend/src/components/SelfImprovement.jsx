import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { apiFetch } from '../services/api'
import { useWebSocketEvent } from '../hooks/useWebSocket'
import '../styles/selfImprovement.css'

const API_BASE = '/api'
const sections = [
  ['overview', 'Overview'], ['data', 'Data readiness'], ['experiment', 'Experiment builder'],
  ['runs', 'Active runs'], ['results', 'Results lab'], ['promotion', 'Shadow / paper'], ['history', 'History'],
]

const asMap = (rows) => Object.fromEntries((Array.isArray(rows) ? rows : []).map(row => [row.key, row.value]))
const splitSymbols = value => String(value || '').split(',').map(v => v.trim().toUpperCase()).filter(Boolean)
const pct = value => `${Math.round(Math.max(0, Math.min(1, Number(value) || 0)) * 100)}%`
const metric = value => value?.available ? Number(value.value).toFixed(3) : 'n/a'
const dateText = value => value ? new Date(value).toLocaleString() : '—'

function statusTone(status) {
  if (['completed', 'passed', 'ready', true].includes(status)) return 'success'
  if (['failed', 'blocked', 'rejected', false].includes(status)) return 'danger'
  return 'warning'
}

function Gate({ passed, label, detail }) {
  return (
    <div className={`si-gate ${passed ? 'pass' : 'fail'}`}>
      <span className="si-gate-icon">{passed ? '✓' : '×'}</span>
      <span><strong>{label}</strong>{detail && <small>{detail}</small>}</span>
    </div>
  )
}

function MetricCard({ label, value, hint, tone = '' }) {
  return <div className={`si-metric ${tone}`}><span>{label}</span><strong>{value}</strong>{hint && <small>{hint}</small>}</div>
}

function ResultTable({ job }) {
  const comparison = job?.comparison
  if (comparison?.rows?.length) {
    return (
      <div className="si-table-wrap"><table className="si-table"><thead><tr><th>Strategy</th><th>Return</th><th>Sharpe</th><th>Drawdown</th><th>Trades</th><th>Costs</th></tr></thead>
        <tbody>{comparison.rows.map(row => <tr key={`${row.strategy_id}-${row.strategy_version}`}>
          <td><strong>{row.strategy_id}</strong><small>{row.strategy_version}</small></td>
          <td>{metric(row.metrics?.total_return)}</td><td>{metric(row.metrics?.sharpe)}</td>
          <td>{metric(row.metrics?.max_drawdown)}</td><td>{row.metrics?.trade_count ?? '—'}</td><td>{row.metrics?.total_costs ?? '—'}</td>
        </tr>)}</tbody>
      </table></div>
    )
  }
  const summary = job?.summary
  if (!summary) return <div className="si-empty">Select a completed run to inspect its evidence.</div>
  const rows = [['Baseline', summary.baseline?.metrics], ['Volatility sizing', summary.vol_sizing?.metrics]]
  return <div className="si-table-wrap"><table className="si-table"><thead><tr><th>Lane</th><th>Sharpe</th><th>Drawdown</th><th>Profit factor</th><th>Trades</th></tr></thead>
    <tbody>{rows.map(([name, metrics]) => <tr key={name}><td><strong>{name}</strong></td><td>{metrics?.Sharpe?.toFixed?.(3) ?? 'n/a'}</td><td>{metrics?.MaxDrawdown?.toFixed?.(3) ?? 'n/a'}</td><td>{metrics?.ProfitFactor?.toFixed?.(2) ?? 'n/a'}</td><td>{metrics?.TradeCount ?? '—'}</td></tr>)}</tbody>
  </table></div>
}

export default function SelfImprovement() {
  const [section, setSection] = useState('overview')
  const [settings, setSettings] = useState({})
  const [strategies, setStrategies] = useState([])
  const [jobs, setJobs] = useState([])
  const [readiness, setReadiness] = useState(null)
  const [operational, setOperational] = useState(null)
  const [governance, setGovernance] = useState(null)
  const [selectedJobID, setSelectedJobID] = useState('')
  const [selectedStrategy, setSelectedStrategy] = useState('trend_momentum_candidate@1.0.0')
  const [parameters, setParameters] = useState({})
  const [hypothesis, setHypothesis] = useState('Trend and momentum selection should outperform normalized market baselines after costs.')
  const [targetGross, setTargetGross] = useState('1')
  const [maxNet, setMaxNet] = useState('1')
  const [finalPolicy, setFinalPolicy] = useState('liquidate')
  const [loading, setLoading] = useState(true)
  const [running, setRunning] = useState(false)
  const [optimizing, setOptimizing] = useState(false)
  const [dispatching, setDispatching] = useState(false)
  const [startingBatch, setStartingBatch] = useState(false)
  const [draftCount, setDraftCount] = useState(4)
  const [experimentDrafts, setExperimentDrafts] = useState([])
  const [notice, setNotice] = useState(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const [settingsRes, strategiesRes, jobsRes, opsRes, governanceRes] = await Promise.all([
        apiFetch(`${API_BASE}/settings`), apiFetch(`${API_BASE}/backtest/strategies`),
        apiFetch(`${API_BASE}/backtest/jobs?limit=200`), apiFetch(`${API_BASE}/operations/status`), apiFetch(`${API_BASE}/settings/governance`),
      ])
      const [settingsJSON, strategiesJSON, jobsJSON] = await Promise.all([settingsRes.json(), strategiesRes.json(), jobsRes.json()])
      const nextSettings = asMap(settingsJSON)
      const nextJobs = Array.isArray(jobsJSON) ? jobsJSON : []
      if (nextSettings.backtest_dataset_manifest_id && (!nextSettings.backtest_start || !nextSettings.backtest_end || !nextSettings.backtest_symbols)) {
        const manifestRes = await apiFetch(`${API_BASE}/market-data/manifests/${nextSettings.backtest_dataset_manifest_id}`)
        if (manifestRes.ok) {
          const manifest = await manifestRes.json()
          nextSettings.backtest_start ||= manifest.requested_start
          nextSettings.backtest_end ||= manifest.requested_end
          nextSettings.backtest_symbols ||= [...new Set((manifest.series || []).filter(item => item.role === 'decision' && item.timeframe === '15m').map(item => item.ticker))].join(',')
        }
      }
      setSettings(nextSettings)
      setStrategies(strategiesJSON.strategies || [])
      setJobs(nextJobs)
      setSelectedJobID(prev => prev || (nextJobs[0] ? String(nextJobs[0].id) : ''))
      setOperational(await opsRes.json())
      if (governanceRes.ok) setGovernance(await governanceRes.json())

      const manifest = nextSettings.backtest_dataset_manifest_id
      const symbols = nextSettings.backtest_symbols
      const start = nextSettings.backtest_start
      const end = nextSettings.backtest_end
      if (manifest && symbols && start && end) {
        const query = new URLSearchParams({ manifest_id: manifest, symbols, start, end, benchmark: 'BTCUSDT', timeframe: nextSettings.decision_timeframe || '15m' })
        const readyRes = await apiFetch(`${API_BASE}/market-data/readiness?${query}`)
        const readyJSON = await readyRes.json()
        setReadiness(readyRes.ok ? readyJSON : { passed: false, failures: [{ code: 'readiness_unavailable', details: readyJSON.error || `HTTP ${readyRes.status}` }] })
      } else {
        setReadiness({ passed: false, failures: [{ code: 'configuration_incomplete', details: 'Manifest, symbols, start and end must be configured.' }] })
      }
    } catch (error) {
      setNotice({ tone: 'danger', text: `Unable to load self-improvement evidence: ${error.message}` })
    } finally { setLoading(false) }
  }, [])

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => {
    const descriptor = strategies.find(item => `${item.id}@${item.version}` === selectedStrategy)
    if (!descriptor) return
    const defaults = Object.fromEntries((descriptor.parameters || []).map(item => [item.name, item.default]))
    setParameters(defaults)
    setTargetGross(String(defaults.target_gross ?? defaults.max_gross ?? '1'))
    setMaxNet(String(defaults.max_net ?? defaults.max_gross ?? '1'))
    setFinalPolicy(String(defaults.final_policy || 'liquidate'))
  }, [selectedStrategy, strategies])

  const updateJob = useCallback(data => {
    const id = data?.id || data?.job_id
    if (!id) return
    setJobs(previous => {
      const next = previous.some(job => job.id === id)
        ? previous.map(job => job.id === id ? { ...job, ...data, id } : job)
        : [{ ...data, id }, ...previous]
      return next.sort((a, b) => new Date(b.created_at || 0) - new Date(a.created_at || 0))
    })
  }, [])
  useWebSocketEvent('backtest_status', updateJob)
  useWebSocketEvent('backtest_progress', updateJob)
  useWebSocketEvent('backtest_complete', data => { updateJob(data); refresh() })

  const selectedJob = jobs.find(job => String(job.id) === selectedJobID) || jobs[0]
  const descriptor = strategies.find(item => `${item.id}@${item.version}` === selectedStrategy)
  const candidateBaseParameters = { ...parameters, target_gross: targetGross, max_net: maxNet, final_policy: finalPolicy, execution_intent: 'backtest' }
  const symbols = splitSymbols(settings.backtest_symbols)
  const activeJob = jobs.find(job => ['pending', 'queued', 'running'].includes(job.status))
  const failures = readiness?.failures || []
  const decisionRows = readiness?.decision_rows_per_symbol || readiness?.decision_rows || {}
  const executionRows = readiness?.execution_rows_per_symbol || readiness?.execution_rows || {}
  const readinessChecks = [
    [Number(readiness?.calendar_months || 0) >= 21, 'Minimum 21 calendar months', `${readiness?.calendar_months ?? 0} available`],
    [symbols.length >= 8, 'Minimum 8 symbols', `${symbols.length} configured`],
    [Object.values(decisionRows).length >= 8, '15m decision coverage', `${Object.values(decisionRows).length} series`],
    [Object.values(executionRows).length >= 8, '1m execution coverage', `${Object.values(executionRows).length} series`],
    [Number(readiness?.fold_count || 0) >= 3, 'Three independent folds', `${readiness?.fold_count ?? 0} available`],
    [Number(readiness?.universe_snapshots || 0) > 0, 'Point-in-time universe', `${readiness?.universe_snapshots ?? 0} snapshots`],
    [Object.keys(readiness?.regime_snapshots || {}).length >= 2, 'Multiple regimes', `${Object.keys(readiness?.regime_snapshots || {}).length} regimes`],
  ]
  const readinessScore = Math.round(readinessChecks.filter(item => item[0]).length / readinessChecks.length * 100)
  const latestPassed = Boolean(selectedJob?.comparison?.governance?.optimization_allowed || selectedJob?.summary?.validation?.passed)
  const recommended = !readiness?.passed ? 'Complete dataset readiness' : !latestPassed ? 'Run a controlled candidate experiment' : 'Review evidence and create Stage 07 validation'

  async function startExperiment() {
    if (!readiness?.passed) {
      setNotice({ tone: 'warning', text: 'Experiment blocked: the immutable research-readiness gate has not passed.' }); return
    }
    if (!hypothesis.trim()) { setNotice({ tone: 'warning', text: 'Write a falsifiable hypothesis before starting.' }); return }
    const [strategyID, strategyVersion] = selectedStrategy.split('@')
    setRunning(true); setNotice(null)
    try {
      const response = await apiFetch(`${API_BASE}/backtest/compare`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ strategy_id: strategyID, strategy_version: strategyVersion, parameters: { ...parameters, execution_intent: 'backtest', max_net: maxNet }, target_gross_exposure: targetGross, max_net_exposure: maxNet, final_policy: finalPolicy }),
      })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      updateJob(data); setSelectedJobID(String(data.id)); setSection('runs')
      setNotice({ tone: 'success', text: `Experiment job #${data.id} was accepted. The hypothesis remains operator context; all economic inputs are immutable in the job artifact.` })
    } catch (error) { setNotice({ tone: 'danger', text: `Experiment was not started: ${error.message}` }) }
    finally { setRunning(false) }
  }

  async function dispatchExperiments() {
    if (!readiness?.passed || !hypothesis.trim()) return
    const [strategyID, strategyVersion] = selectedStrategy.split('@')
    const baseParameters = { ...parameters, target_gross: targetGross, max_net: maxNet, final_policy: finalPolicy, execution_intent: 'backtest' }
    setDispatching(true); setNotice(null)
    try {
      const response = await apiFetch(`${API_BASE}/ai/dispatch-experiments`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ hypothesis, strategy_id: strategyID, strategy_version: strategyVersion, count: Number(draftCount), base_parameters: baseParameters }),
      })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      setExperimentDrafts((data.experiments || []).map((draft, index) => ({ ...draft, selected: true, local_id: `${Date.now()}-${index}` })))
      setNotice({ tone: 'success', text: `${data.count} advisory experiment drafts generated. Review them before submitting the batch.` })
    } catch (error) { setNotice({ tone: 'danger', text: `AI dispatch failed: ${error.message}` }) }
    finally { setDispatching(false) }
  }

  async function startExperimentBatch() {
    const selected = experimentDrafts.filter(draft => draft.selected)
    if (!selected.length) return
    const [strategyID, strategyVersion] = selectedStrategy.split('@')
    setStartingBatch(true); setNotice(null)
    try {
      const response = await apiFetch(`${API_BASE}/backtest/compare/batch`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ experiments: selected.map(draft => ({
          strategy_id: strategyID, strategy_version: strategyVersion, parameters: draft.parameters,
          target_gross_exposure: draft.parameters.target_gross || targetGross,
          max_net_exposure: draft.parameters.max_net || maxNet,
          final_policy: draft.parameters.final_policy || finalPolicy,
        })) }),
      })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      ;(data.jobs || []).forEach(updateJob)
      setExperimentDrafts([]); setSection('runs')
      setNotice({ tone: 'success', text: `${data.count} experiments queued. At most ${data.concurrency_limit} run concurrently to protect memory.` })
    } catch (error) { setNotice({ tone: 'danger', text: `Experiment batch was not started: ${error.message}` }) }
    finally { setStartingBatch(false) }
  }

  async function proposeNext() {
    if (!selectedJob?.id) return
    setOptimizing(true); setNotice(null)
    try {
      const response = await apiFetch(`${API_BASE}/ai/optimize-backtest`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ job_id: selectedJob.id }) })
      const data = await response.json()
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      setNotice({ tone: data.count > 0 ? 'success' : 'warning', text: data.message || `Created ${data.count || 0} governed proposal(s). Review them in AI Proposals before applying anything.` })
    } catch (error) { setNotice({ tone: 'danger', text: `Proposal generation failed: ${error.message}` }) }
    finally { setOptimizing(false) }
  }

  if (loading && jobs.length === 0) return <div className="glass-panel si-loading">Loading immutable research evidence…</div>

  return <div className="si-page">
    <div className="si-hero">
      <div><p className="si-kicker">CONTROLLED RESEARCH LOOP</p><h2>Self Improvement</h2><p>Improve configurations through reproducible experiments. The system may propose; only an operator may promote.</p></div>
      <div className="si-authority"><span>Authority</span><strong>RESEARCH ONLY</strong><small>{operational?.status ? `Platform: ${operational.status}` : 'Human-controlled promotion'}</small></div>
    </div>
    <nav className="si-tabs" aria-label="Self improvement sections">{sections.map(([key, label]) => <button key={key} className={section === key ? 'active' : ''} onClick={() => setSection(key)}>{label}</button>)}</nav>
    {notice && <div className={`si-notice ${notice.tone}`} role="status"><span>{notice.text}</span><button onClick={() => setNotice(null)} aria-label="Dismiss notification">×</button></div>}

    {section === 'overview' && <>
      <div className="si-metrics-grid"><MetricCard label="Evidence readiness" value={`${readinessScore}%`} hint={readiness?.passed ? 'All immutable gates passed' : `${failures.length} reported blockers`} tone={readiness?.passed ? 'success' : 'warning'} /><MetricCard label="Current stage" value={activeJob ? 'EXPERIMENT RUNNING' : readiness?.passed ? 'EXPERIMENT DESIGN' : 'DATA EXPANSION'} hint={activeJob ? `Job #${activeJob.id}` : 'No capital authority'} /><MetricCard label="Latest decision" value={latestPassed ? 'REVIEW' : 'RESEARCH ONLY'} hint={selectedJob ? `Based on job #${selectedJob.id}` : 'No completed evidence'} tone={latestPassed ? 'success' : 'danger'} /><MetricCard label="Configured universe" value={`${symbols.length} symbols`} hint={`${settings.decision_timeframe || '15m'} decisions · 1m execution`} /></div>
      <div className="si-two-col"><section className="glass-panel"><div className="si-panel-title"><div><span>Recommended next step</span><h3>{recommended}</h3></div><button className="si-primary" onClick={() => setSection(readiness?.passed ? 'experiment' : 'data')}>Continue</button></div><div className="si-flow">{['Data', 'Hypothesis', 'Backtest', 'Validation', 'Human review', 'Shadow'].map((item, index) => <React.Fragment key={item}><div className={index === 0 || readiness?.passed ? 'done' : ''}><b>{index + 1}</b><span>{item}</span></div>{index < 5 && <i>→</i>}</React.Fragment>)}</div><p className="si-explainer">The learning result is a new immutable parameter proposal with evidence—not a silent code change and never an automatic live promotion.</p></section><section className="glass-panel"><div className="si-panel-title"><div><span>Latest evidence</span><h3>{selectedJob ? `Backtest #${selectedJob.id}` : 'No backtests'}</h3></div><span className={`si-pill ${statusTone(selectedJob?.status)}`}>{selectedJob?.status || 'missing'}</span></div><ResultTable job={selectedJob} /></section></div>
    </>}

    {section === 'data' && <div className="si-two-col si-data"><section className="glass-panel"><div className="si-panel-title"><div><span>Immutable input</span><h3>Dataset readiness</h3></div><button className="si-secondary" onClick={refresh}>Refresh evidence</button></div><div className="si-readiness-ring" style={{ '--score': `${readinessScore * 3.6}deg` }}><strong>{readinessScore}%</strong><span>{readiness?.passed ? 'READY' : 'BLOCKED'}</span></div><div className="si-data-meta"><div><span>Interval</span><strong>{settings.backtest_start || '—'} → {settings.backtest_end || '—'}</strong></div><div><span>Manifest</span><code>{settings.backtest_dataset_manifest_id ? `${settings.backtest_dataset_manifest_id.slice(0, 12)}…` : 'missing'}</code></div><div><span>Policy</span><strong>{readiness?.policy?.version || 'research-readiness-v1'}</strong></div><div><span>Folds</span><strong>{readiness?.fold_count ?? 0} / {readiness?.policy?.min_folds ?? 3}</strong></div></div></section><section className="glass-panel"><div className="si-panel-title"><div><span>Hard gates</span><h3>What must be true</h3></div><span className={`si-pill ${readiness?.passed ? 'success' : 'danger'}`}>{readiness?.passed ? 'PASSED' : 'FAIL CLOSED'}</span></div><div className="si-gates">{readinessChecks.map(([pass, label, detail]) => <Gate key={label} passed={pass} label={label} detail={detail} />)}</div>{failures.length > 0 && <details className="si-failures"><summary>{failures.length} detailed blocker(s)</summary>{failures.map((failure, i) => <div key={`${failure.code}-${i}`}><code>{failure.code}</code><span>{failure.subject || failure.details}</span></div>)}</details>}<p className="si-boundary">Data ingestion remains an operator/bootstrap workflow because the web server does not receive migration credentials.</p></section></div>}

    {section === 'experiment' && <div className="si-two-col"><section className="glass-panel"><div className="si-panel-title"><div><span>Step 2</span><h3>Falsifiable hypothesis</h3></div><span className={`si-pill ${readiness?.passed ? 'success' : 'danger'}`}>{readiness?.passed ? 'DATA READY' : 'BLOCKED'}</span></div>{!readiness?.passed && <div className="si-blocker"><strong>Why this is blocked</strong><p>{failures[0]?.details || 'The immutable data-readiness gate has not passed.'}</p><button className="si-secondary" onClick={() => setSection('data')}>Review data gates</button></div>}<label className="si-field"><span>What should improve, and against which baseline?</span><textarea value={hypothesis} onChange={event => setHypothesis(event.target.value)} rows="5" /></label><div className="si-rule"><strong>Selection contract</strong><p>Tune on train/validation, freeze the decision, then evaluate untouched OOS folds. Positive nominal return alone never passes.</p></div></section><section className="glass-panel"><div className="si-panel-title"><div><span>Step 3</span><h3>Controlled candidate</h3></div></div><label className="si-field"><span>Registered strategy</span><select value={selectedStrategy} onChange={event => setSelectedStrategy(event.target.value)}>{strategies.filter(item => item.research_only).map(item => <option key={`${item.id}@${item.version}`} value={`${item.id}@${item.version}`}>{item.id}@{item.version}</option>)}</select></label><p className="si-description">{descriptor?.description}</p><div className="si-param-grid">{(descriptor?.parameters || []).filter(item => !['execution_intent', 'final_policy', 'target_gross'].includes(item.name)).map(spec => <label className="si-field" key={spec.name}><span>{spec.name}</span>{spec.enum?.length ? <select value={parameters[spec.name] || spec.default} onChange={e => setParameters(p => ({ ...p, [spec.name]: e.target.value }))}>{spec.enum.map(value => <option key={value}>{value}</option>)}</select> : <input value={parameters[spec.name] ?? spec.default ?? ''} type={spec.type === 'integer' || spec.type === 'number' ? 'number' : 'text'} min={spec.minimum} max={spec.maximum} onChange={e => setParameters(p => ({ ...p, [spec.name]: e.target.value }))} />}</label>)}</div><div className="si-param-grid compact"><label className="si-field"><span>Target gross</span><input value={targetGross} onChange={e => setTargetGross(e.target.value)} /></label><label className="si-field"><span>Maximum net</span><input value={maxNet} onChange={e => setMaxNet(e.target.value)} /></label><label className="si-field"><span>Final positions</span><select value={finalPolicy} onChange={e => setFinalPolicy(e.target.value)}><option value="liquidate">Liquidate</option><option value="mark_to_market">Mark to market</option></select></label></div><div className="si-ai-dispatch"><div className="si-ai-dispatch-head"><div><span>AI experiment dispatch</span><strong>Generate a bounded set of different candidates</strong></div><label><span>Drafts</span><input type="number" min="1" max="8" value={draftCount} onChange={event => setDraftCount(Math.max(1, Math.min(8, Number(event.target.value) || 1)))} /></label></div><p>The configured LLM creates advisory research drafts only. Every parameter set is validated by the canonical strategy registry before it can run.</p><button className="si-secondary si-wide" disabled={dispatching || !readiness?.passed} onClick={dispatchExperiments}>{dispatching ? 'Asking configured LLM…' : 'Generate experiment set with AI'}</button>{experimentDrafts.length > 0 && <div className="si-draft-list">{experimentDrafts.map((draft, index) => { const changes = Object.entries(draft.parameters || {}).filter(([key, value]) => String(candidateBaseParameters[key] ?? '') !== String(value)); return <label className={`si-draft ${draft.selected ? 'selected' : ''}`} key={draft.local_id}><input type="checkbox" checked={draft.selected} onChange={event => setExperimentDrafts(current => current.map(item => item.local_id === draft.local_id ? { ...item, selected: event.target.checked } : item))} /><span><strong>{index + 1}. {draft.name}</strong><small>{draft.hypothesis}</small><small>{draft.rationale}</small><em>{changes.length ? changes.map(([key, value]) => `${key}: ${candidateBaseParameters[key] ?? '—'} → ${value}`).join(' · ') : 'No effective change'}</em></span></label>})}<button className="si-primary si-wide" disabled={startingBatch || !experimentDrafts.some(draft => draft.selected)} onClick={startExperimentBatch}>{startingBatch ? 'Queueing experiments…' : `Review complete — start ${experimentDrafts.filter(draft => draft.selected).length} selected`}</button></div>}</div><div className="si-or"><span>or run only the manually configured candidate</span></div><button className="si-primary si-wide" disabled={running || !readiness?.passed} onClick={startExperiment}>{running ? 'Submitting immutable experiment…' : 'Approve and start experiment'}</button></section></div>}

    {section === 'runs' && <div className="si-two-col"><section className="glass-panel"><div className="si-panel-title"><div><span>Execution center</span><h3>{activeJob ? `Active job #${activeJob.id}` : 'No active job'}</h3></div><button className="si-secondary" onClick={refresh}>Refresh</button></div>{activeJob ? <><div className="si-progress-head"><span>{activeJob.message || activeJob.job_type}</span><strong>{pct(activeJob.progress)}</strong></div><div className="si-progress"><i style={{ width: pct(activeJob.progress) }} /></div><div className="si-data-meta"><div><span>Status</span><strong>{activeJob.status}</strong></div><div><span>Started</span><strong>{dateText(activeJob.started_at || activeJob.created_at)}</strong></div><div><span>Manifest</span><code>{activeJob.dataset_manifest_id?.slice?.(0, 12) || 'pending'}</code></div><div><span>Type</span><strong>{activeJob.job_type}</strong></div></div></> : <div className="si-empty">The research queue is idle. Start only after data readiness passes.</div>}</section><section className="glass-panel"><div className="si-panel-title"><div><span>Recent queue</span><h3>Backtest jobs</h3></div></div><div className="si-job-list">{jobs.slice(0, 12).map(job => <button key={job.id} className={String(job.id) === selectedJobID ? 'selected' : ''} onClick={() => { setSelectedJobID(String(job.id)); setSection('results') }}><span className={`si-status-dot ${statusTone(job.status)}`} /><span><strong>#{job.id} · {job.job_type}</strong><small>{dateText(job.created_at)}</small></span><em>{job.status}</em></button>)}</div></section></div>}

    {section === 'results' && <><section className="glass-panel"><div className="si-panel-title"><div><span>Results lab</span><h3>Comparable evidence</h3></div><div className="si-actions"><select aria-label="Selected backtest" value={selectedJobID} onChange={e => setSelectedJobID(e.target.value)}>{jobs.map(job => <option key={job.id} value={job.id}>#{job.id} · {job.status} · {job.job_type}</option>)}</select><button className="si-secondary" disabled={optimizing || !selectedJob?.summary} onClick={proposeNext}>{optimizing ? 'Generating…' : 'Propose next experiment'}</button></div></div><ResultTable job={selectedJob} /><div className="si-result-footer"><div><span>Artifact digest</span><code>{selectedJob?.artifact_digest || 'not available'}</code></div><div><span>Manifest</span><code>{selectedJob?.dataset_manifest_id || selectedJob?.comparison?.manifest_id || 'not available'}</code></div></div></section><div className="si-two-col si-result-gates"><section className="glass-panel"><div className="si-panel-title"><div><span>Decision</span><h3>{latestPassed ? 'Eligible for human review' : 'Remain research only'}</h3></div><span className={`si-pill ${latestPassed ? 'success' : 'danger'}`}>{latestPassed ? 'REVIEW' : 'REJECT / ITERATE'}</span></div><Gate passed={Boolean(selectedJob?.comparison?.governance?.optimization_allowed)} label="Baseline-relative optimization gate" detail={(selectedJob?.comparison?.governance?.reasons || []).join(' · ') || 'No Stage 05 comparison evidence'} /><Gate passed={Boolean(selectedJob?.summary?.validation?.passed)} label="Walk-forward validation" detail={selectedJob?.summary?.validation?.recommended_stage || 'Not passed'} /></section><section className="glass-panel"><h3>Next learning cycle</h3><p className="si-explainer">Generate proposals only from a completed artifact. Review the proposal, clone it into a new experiment, and change one hypothesis at a time.</p><button className="si-primary" disabled={optimizing || !selectedJob?.summary} onClick={proposeNext}>Generate governed proposal</button></section></div></>}

    {section === 'promotion' && <div className="si-two-col"><section className="glass-panel"><div className="si-panel-title"><div><span>Human-controlled rollout</span><h3>Shadow and paper progression</h3></div><span className="si-pill warning">NO AUTO-PROMOTION</span></div><div className="si-rollout">{['research', 'shadow', 'paper', 'live'].map((stage, index) => <React.Fragment key={stage}><div className={stage === (governance?.context?.rollout_state || 'research') ? 'current' : index === 0 ? 'done' : ''}><b>{index + 1}</b><span>{stage}</span></div>{index < 3 && <i>→</i>}</React.Fragment>)}</div><p className="si-explainer">The platform context is currently <strong>{governance?.context?.rollout_state || 'research'}</strong>. This does not promote the candidate selected on this page; every candidate needs its own immutable Stage 07 evidence and approval.</p><div className="si-data-meta"><div><span>Experiment</span><code>{governance?.context?.experiment_id || 'none'}</code></div><div><span>Policy bundle</span><code>{governance?.context?.policy_versions?.composite_version || 'none'}</code></div><div><span>Model</span><strong>{governance?.context?.model_version || 'rule based'}</strong></div><div><span>Fallback</span><strong>{governance?.context?.fallback_mode || 'fail closed'}</strong></div></div></section><section className="glass-panel"><div className="si-panel-title"><div><span>Candidate eligibility</span><h3>Required before review</h3></div></div><div className="si-gates"><Gate passed={Boolean(readiness?.passed)} label="Dataset readiness" detail={readiness?.passed ? 'Immutable evidence complete' : 'Data gate blocked'} /><Gate passed={Boolean(selectedJob?.comparison?.governance?.optimization_allowed)} label="Baseline-relative gate" detail="Candidate must outperform normalized baselines" /><Gate passed={Boolean(selectedJob?.summary?.validation?.passed)} label="Stage 07 walk-forward" detail="Purged, frozen, independent OOS folds" /><Gate passed={false} label="Human approval" detail="Authenticated governance approval is always required" /></div><button className="si-primary si-wide" disabled>Promotion unavailable until every gate passes</button></section></div>}

    {section === 'history' && <section className="glass-panel"><div className="si-panel-title"><div><span>Immutable lineage</span><h3>Research history</h3></div><span>{jobs.length} stored jobs</span></div><div className="si-timeline">{jobs.map(job => <button key={job.id} onClick={() => { setSelectedJobID(String(job.id)); setSection('results') }}><i className={statusTone(job.status)} /><span><strong>#{job.id} · {job.job_type}</strong><small>{dateText(job.created_at)} · {job.status}</small><small>{job.message || job.error || 'Immutable result stored'}</small></span><em>{job.summary?.validation?.passed || job.comparison?.governance?.optimization_allowed ? 'review' : 'research'}</em></button>)}</div></section>}
  </div>
}
