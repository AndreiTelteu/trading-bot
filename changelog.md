# Changelog

Acest fișier păstrează un rezumat durabil al modificărilor și al rezultatelor de cercetare relevante. Artifactele canonice complete rămân în PostgreSQL și în spațiul privat al rulării; valorile de aici sunt un index pentru audit și nu înlocuiesc artifactele imuabile.

## 2026-10-01 — Decision Council v2 și Stage 07 v4

### Configurația evaluată

Configurația înghețată pentru evaluarea finală din această iterație:

- strategie: `trend_momentum_candidate@1.4.0`;
- policy: `decision_council_policy=active_v2`;
- model: `decider/decider-4b`;
- entry: alegere finală `admit`, `bull >= 0.70`, `bear < 0.50`;
- early exit: `decision_council_early_exit=false`;
- engine: `backtest-execution-v4`;
- final-position policy: `mark_to_market`;
- interval Stage 05: `2024-12-01T00:00:00Z`–`2026-09-01T00:00:00Z`;
- dataset manifest: `32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc`;
- implementation digest candidat: `3ad180b912257a9608256216889e9167efa913bfd07542f00cb9348302c24821`;
- config digest candidat: `94b8b16e96c882908c4a72385ef0cd68599e8d727fad40ad2ac3ced9830e62e6`.

### Rezultate Stage 05

#### Configurația inițială — job 100

- status: `completed`;
- artifact: `5f0da6abb890518f1812342d53e44413538ca88af7adcd4ac7ed87bf59ff4a7e`;
- return după costuri: `-1.6814%`;
- equity final: `983.1858`;
- max drawdown: `13.4487%`;
- Sharpe: `-0.0287`;
- trades: `303`;
- council admits: `919`.

Concluzie: regula inițială a fost prea permisivă și a rămas sub cash și sub matched baseline.

#### Entry selectiv și early exit activ — job 102

- status: `completed`;
- artifact: `c4b21cff736adbb7a705344bf63401925284f51b596bafc883039d4f614ee9eb`;
- return după costuri: `+1.1219%`;
- equity final: `1011.2188`;
- max drawdown: `14.0555%`;
- Sharpe: `0.1156`;
- trades: `299`;
- council admits: `15`;
- council early exits: `4`.

Cele patru early exits au fost premature post-hoc; activele au avut randament mediu pozitiv după exit, inclusiv aproximativ `+2.05%` la șase bare.

#### Entry selectiv, fără early exit — joburile 103–105

Joburile `103`, `104` și `105` au produs exact aceleași artifacte:

- comparison artifact: `396b2aedd19f117ef5692d45c783294ca0111b72c68b1e416d6e9a616d8a88cd`;
- Stage 07 source artifact: `dce8690cadea93f4782acc189d9cb3aee7a70d0342a346110b21072092dd949d`;
- return după costuri: `+1.9471%`;
- equity final: `1019.4708`;
- max drawdown: `14.0555%`;
- Sharpe: `0.1583`;
- Sortino: `0.2207`;
- profit factor: `1.0271`;
- expectancy: `+0.0508`;
- trades: `300`;
- costuri: `95.6207`;
- council admits: `16`;
- held-position council traces: `0`;
- council early exits: `0`.

Matched baseline pe același replay:

- return: `+1.2642%`;
- equity final: `1012.6420`;
- max drawdown: `13.7333%`;
- Sharpe: `0.1237`;
- costuri: `89.2638`.

Configurația entry-only a depășit matched baseline cu aproximativ `+0.6829` puncte procentuale în replay-ul Stage 05. Reproducerile identice confirmă determinismul tehnic, dar nu reprezintă trei eșantioane statistice independente.

### Stage 07 v4 retrospectiv și exploratoriu

A fost adăugat un driver separat `-stage07-track v4`. Contractul istoric Stage 07 v3 a rămas separat și nu a fost relaxat.

Driverul v4:

- admite numai sursele auditate `103,104,105`;
- verifică digesturile candidatei, baseline-ului, datasetului și artifactelor sursă;
- îngheață toți parametrii strategiei la o singură alegere;
- verifică semanticile `backtest-execution-v4`, fill-urile, costurile, capacitatea și no-fill-urile;
- folosește trei ferestre cronologice, purge și embargo de 4h și 500 iterații bootstrap;
- etichetează studiul `exploratory` și raportează întotdeauna `promotion_authorized=false`.

Run final:

- experiment: `2e1809db82289d112939700a964486c6a1ed8a429860e90388f7aa3e77affa90`;
- evidence: `e00995afd73065f1a2c0dfaf43a02f9578b6de3aba981099165076b6bab705ec`;
- status operațional: `passed`;
- promotion authorized: `false`.

`passed` înseamnă că evaluarea și persistența evidenței au fost complete și valide. Nu înseamnă că gate-urile economice au trecut.

#### Rezultate agregate Stage 07 v4

- return mediu după costuri: `-1.0321%`;
- bootstrap lower bound pentru return: `-3.0434%`;
- benchmark-relative return mediu: `-0.4179%`;
- benchmark-relative lower bound: `-1.0051%`;
- stressed return mediu: `-1.0831%`;
- deflated Sharpe mediu: `-0.0042`;
- max drawdown mediu: `9.1509%`;
- turnover mediu: `4.6291x`;
- coverage: `100%`;
- unități cronologice: `3`.

Gate-uri economice:

| Gate | Rezultat |
|---|---|
| `after_cost_return > 0` | fail |
| `benchmark_relative_return > 0` | fail |
| `coverage >= 1` | pass |
| `max_drawdown <= 0.20` | pass |
| `stressed_after_cost_return > 0` | fail |

#### Rezultate per fold

| Fold | Interval test | Return după costuri | Față de baseline | Sharpe | Max drawdown | Trades |
|---|---|---:|---:|---:|---:|---:|
| 0 | 2026-01-01–2026-03-01 | `-3.0434%` | `-0.0490 pp` | `-0.0118` | `6.4093%` | 22 |
| 1 | 2026-04-01–2026-06-01 | `-1.3723%` | `-0.1997 pp` | `-0.0061` | `2.9787%` | 36 |
| 2 | 2026-07-01–2026-08-31 | `+1.3194%` | `-1.0051 pp` | `+0.0053` | `3.9781%` | 40 |

Niciun fold nu a depășit matched baseline. Avantajul observat în replay-ul Stage 05 complet nu a fost stabil cronologic. Worst regime a fost `risk_on`, iar worst symbol a fost `AVAXUSDT`.

### Modificări tehnice recente

- Decision Council v2 cu policy observe/active și trace-uri auditabile.
- Provideri adăugați:
  - `tokenrouter/typesafe/jev-1.13`;
  - `decider/decider-4b`, anonim, fără header `Authorization`.
- Context Decider compactat la schema `decision-council-state-v2-compact-v3`, cu limită strictă de 1.800 bytes pentru state și request complet de aproximativ 3 KB.
- Prompturi separate pentru entry și held-position exit.
- Context economic compact: costuri, edge necesar, rank distance, sloturi, expunere, turnover și MFE giveback.
- Parametru versionat `decision_council_early_exit=true|false`.
- Cu early exit dezactivat, held positions sunt excluse complet din council; ieșirile normale v4 și hard stop-ul rămân autoritative.
- Driver Stage 07 v4 separat, cu plan privat revizuit prin SHA-256 și preflight PostgreSQL manifest-backed.
- Verificarea Stage 07 v4 a fost corectată astfel încât costurile fill-ului să fie reconstruite din close-ul barei, slippage și tick, fără a confunda prețul de referință al deciziei cu close-ul de execuție.
- Contabilizarea no-fill-urilor all-or-none pentru exit a fost corectată pentru a păstra expunerea reziduală în verificările risk-off ulterioare.

### Decizie de cercetare

- Configurația entry-only este deterministică și reproductibilă tehnic.
- Rezultatul Stage 07 retrospectiv este negativ față de cash și matched baseline.
- Configurația nu este autorizată pentru promovare.
- Stage 07 rămâne retrospectiv/in-sample deoarece pragul `bull >= 0.70` a fost ales folosind aceeași perioadă istorică.
- Nu se ajustează pragurile folosind aceste fold-uri.
- Un dataset ulterior, nevăzut și blocat înainte de evaluare, rămâne obligatoriu pentru orice validare confirmatorie; rezultatele curente nu justifică însă consumarea imediată a acelui holdout pentru configurația exactă evaluată.

### Verificare

După modificări au trecut:

- `go test -p 1 -count=1 ./...`;
- `go vet ./...`;
- `git diff --check`;
- evaluarea completă Stage 07 v4 pe clona PostgreSQL izolată.
