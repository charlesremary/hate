// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"math"
	"math/rand"
	"sort"

	"hate/internal/ticket"
)

// Monte Carlo estimate (COSMIC tab). Code hours are drawn per feature from the
// reference h/CFP rates; platform wrap is the fixed sum of wrap-ticket
// estimates. See RunMonteCarlo for the sampling.

// Monte Carlo constants. The seed is fixed so the numbers don't change between
// page loads.
const (
	MonteCarloRuns = 10000
	MonteCarloSeed = 42
	// MonteCarloWidenK widens the log-space spread (calibration from the
	// Phase 0.1 backtest, where the unwidened range was too narrow).
	MonteCarloWidenK = 1.5
	monteCarloBins   = 20
	// ownBlendHalf is the own-feature count at which own and borrowed draws are
	// equally likely; ownBlendFull is the count at which own features take over.
	ownBlendHalf = 5
	ownBlendFull = 15
)

// Monte Carlo error states (reported in MonteCarloResult.Error).
const (
	MCErrNoReference = "no reference selected"
	MCErrNoFeatures  = "no cfp features in this project"
	MCErrTooFewRefs  = "need at least 3 reference features"
)

// MCFeature is one target feature: its size, and for a done feature its actual
// functional hours.
type MCFeature struct {
	ID          string
	CFP         int
	Done        bool    // all functional tickets done (FeatureFunctionalDone)
	ActualHours float64 // functional hours logged (used when Done)
}

// MCInput is everything the engine needs. It is pure data, so the engine is
// deterministic for a given input.
type MCInput struct {
	Features         []MCFeature
	Own              []float64 // h/CFP of this project's reference features
	Borrowed         []float64 // h/CFP of borrowed reference features
	Configured       bool      // any reference option is selected
	BorrowedProjects []string
	MissingProjects  []string
	Wrap             MCPlatformWrap
	UncPct           float64 // CFP counting uncertainty, +/- percent
	ActualHours      float64 // all logged hours on in-scope tickets
	Runs             int     // 0 = MonteCarloRuns
	Seed             int64
}

// MCPercentiles is a P50/P85/P95 range.
type MCPercentiles struct {
	P50 float64 `json:"p50"`
	P85 float64 `json:"p85"`
	P95 float64 `json:"p95"`
}

// MCPlatformWrap is the fixed platform-wrap sum and where its numbers came from.
type MCPlatformWrap struct {
	Hours          float64 `json:"hours"`
	TicketCount    int     `json:"ticket_count"`
	ActualCount    int     `json:"actual_count"`    // done tickets: logged hours used instead of the estimate
	ConvertedCount int     `json:"converted_count"` // legacy effort only: flagged, not summed
	MissingCount   int     `json:"missing_count"`   // no estimate at all
}

// MCProjectedFinish is the code projection from progress so far: actual
// functional hours on done features plus the simulated code hours of the
// features not done yet.
type MCProjectedFinish struct {
	DoneActualHours       float64 `json:"done_actual_hours"`
	RemainingFeatureCount int     `json:"remaining_feature_count"`
	P50                   float64 `json:"p50"`
	P85                   float64 `json:"p85"`
}

// MCBin is one histogram bin over the simulated totals.
type MCBin struct {
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
	Count int     `json:"count"`
}

// MonteCarloResult is the COSMIC tab's estimate. OK is false when the
// estimate can't run; Error then says why.
type MonteCarloResult struct {
	OK               bool              `json:"ok"`
	Error            string            `json:"error"`
	Runs             int               `json:"runs"`
	Seed             int64             `json:"seed"`
	WidenK           float64           `json:"widen_k"`
	NOwn             int               `json:"n_own"`
	NBorrowed        int               `json:"n_borrowed"`
	POwn             float64           `json:"p_own"`
	RefMedianRate    float64           `json:"ref_median_rate"`
	BorrowedProjects []string          `json:"borrowed_projects"`
	MissingProjects  []string          `json:"missing_projects"`
	TotalCFP         int               `json:"total_cfp"`
	FeatureCount     int               `json:"feature_count"`
	Code             MCPercentiles     `json:"code"`
	PlatformWrap     MCPlatformWrap    `json:"platform_wrap"`
	Total            MCPercentiles     `json:"total"`
	ActualHours      float64           `json:"actual_hours"`
	ProjectedFinish  MCProjectedFinish `json:"projected_finish"`
	Histogram        []MCBin           `json:"histogram"`
}

// logNormalFit is a log-space (mu, sd) pair.
type logNormalFit struct{ mu, sd float64 }

// fitLogNormal fits mu = mean(ln r), sd = sample stdev(ln r) (0 with fewer
// than 2 rates), then widens sd by k with mu shifted so the mean rate
// exp(mu + sd^2/2) is unchanged.
func fitLogNormal(rates []float64, k float64) logNormalFit {
	var logs []float64
	for _, r := range rates {
		if r > 0 {
			logs = append(logs, math.Log(r))
		}
	}
	if len(logs) == 0 {
		return logNormalFit{}
	}
	var sum float64
	for _, l := range logs {
		sum += l
	}
	mu := sum / float64(len(logs))
	sd := 0.0
	if len(logs) >= 2 {
		var ss float64
		for _, l := range logs {
			ss += (l - mu) * (l - mu)
		}
		sd = math.Sqrt(ss / float64(len(logs)-1))
	}
	sdW := k * sd
	return logNormalFit{mu: mu + (sd*sd-sdW*sdW)/2, sd: sdW}
}

// OwnBlendProbability is the share of draws taken from the project's own
// features: own_N / (own_N + 5), and 1 once own_N >= 15.
func OwnBlendProbability(nOwn int) float64 {
	if nOwn <= 0 {
		return 0
	}
	if nOwn >= ownBlendFull {
		return 1
	}
	return float64(nOwn) / float64(nOwn+ownBlendHalf)
}

// RunMonteCarlo runs the estimate:
//   - fit a log-normal per reference pool (own, borrowed), widened K = 1.5
//     with the mean preserved;
//   - per run, each target feature's code hours = CFP x exp(N(mu', sd')), the
//     pool picked per draw with probability p_own (own_N/(own_N+5), 1 at 15+;
//     the only non-empty pool when just one has features);
//   - the run's CFP-driven hours are scaled once by U(1-u, 1+u) for the
//     counting uncertainty;
//   - total = code + the fixed platform wrap.
//
// The projected finish reuses each run's draws for the not-done features and
// adds the actual functional hours of the done ones.
func RunMonteCarlo(in MCInput) MonteCarloResult {
	runs := in.Runs
	if runs <= 0 {
		runs = MonteCarloRuns
	}
	res := MonteCarloResult{
		Runs:             runs,
		Seed:             in.Seed,
		WidenK:           MonteCarloWidenK,
		NOwn:             len(in.Own),
		NBorrowed:        len(in.Borrowed),
		BorrowedProjects: nonNilStrings(in.BorrowedProjects),
		MissingProjects:  nonNilStrings(in.MissingProjects),
		PlatformWrap:     in.Wrap,
		ActualHours:      round2(in.ActualHours),
		Histogram:        []MCBin{},
	}
	res.PlatformWrap.Hours = round2(in.Wrap.Hours)
	for _, f := range in.Features {
		res.TotalCFP += f.CFP
		res.FeatureCount++
	}
	all := append(append([]float64{}, in.Own...), in.Borrowed...)
	if len(all) > 0 {
		sort.Float64s(all)
		res.RefMedianRate = math.Round(cosmicMedian(all)*10000) / 10000
	}

	switch {
	case !in.Configured:
		res.Error = MCErrNoReference
		return res
	case len(in.Features) == 0:
		res.Error = MCErrNoFeatures
		return res
	case len(all) < minReferenceFeatures:
		res.Error = MCErrTooFewRefs
		return res
	}

	pOwn := OwnBlendProbability(len(in.Own))
	if len(in.Borrowed) == 0 {
		pOwn = 1
	} else if len(in.Own) == 0 {
		pOwn = 0
	}
	res.POwn = math.Round(pOwn*10000) / 10000
	var doneActual float64
	remaining := 0
	for _, f := range in.Features {
		if f.Done {
			doneActual += f.ActualHours
		} else {
			remaining++
		}
	}
	code, totals, rest := simulateMonteCarlo(in, pOwn, runs)
	finish := make([]float64, runs)
	for i, r := range rest {
		finish[i] = doneActual + r
	}

	res.Code = percentiles(code)
	res.Total = percentiles(totals)
	fp := percentiles(finish)
	res.ProjectedFinish = MCProjectedFinish{
		DoneActualHours:       round2(doneActual),
		RemainingFeatureCount: remaining,
		P50:                   fp.P50,
		P85:                   fp.P85,
	}
	res.Histogram = histogram(totals, monteCarloBins)
	res.OK = true
	return res
}

// simulateMonteCarlo runs the draws and returns, per run, the code hours, the
// total (code + platform wrap), and the code hours of the not-done features.
func simulateMonteCarlo(in MCInput, pOwn float64, runs int) (code, totals, rest []float64) {
	own := fitLogNormal(in.Own, MonteCarloWidenK)
	borrowed := fitLogNormal(in.Borrowed, MonteCarloWidenK)
	u := in.UncPct / 100
	rng := rand.New(rand.NewSource(in.Seed))
	code = make([]float64, runs)
	totals = make([]float64, runs)
	rest = make([]float64, runs)
	for i := 0; i < runs; i++ {
		var sum, open float64
		for _, f := range in.Features {
			fit := borrowed
			if pOwn >= 1 || (pOwn > 0 && rng.Float64() < pOwn) {
				fit = own
			}
			h := float64(f.CFP) * math.Exp(fit.mu+fit.sd*rng.NormFloat64())
			sum += h
			if !f.Done {
				open += h
			}
		}
		if u > 0 { // counting uncertainty: once per run, on all CFP-driven hours
			factor := 1 - u + 2*u*rng.Float64()
			sum *= factor
			open *= factor
		}
		code[i] = sum
		totals[i] = sum + in.Wrap.Hours
		rest[i] = open
	}
	return code, totals, rest
}

// percentiles sorts xs in place and returns its P50/P85/P95 (linear
// interpolation between order statistics).
func percentiles(xs []float64) MCPercentiles {
	sort.Float64s(xs)
	return MCPercentiles{P50: round2(quantile(xs, 0.50)), P85: round2(quantile(xs, 0.85)), P95: round2(quantile(xs, 0.95))}
}

// quantile is the q-th quantile of sorted xs.
func quantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	pos := q * float64(n-1)
	lo := int(math.Floor(pos))
	if lo >= n-1 {
		return sorted[n-1]
	}
	frac := pos - float64(lo)
	return sorted[lo] + frac*(sorted[lo+1]-sorted[lo])
}

// histogram buckets xs into n equal bins from min to max. When every value is
// the same, the range is widened to +/- 0.5h so the bins still have width.
func histogram(xs []float64, n int) []MCBin {
	if len(xs) == 0 {
		return []MCBin{}
	}
	lo, hi := xs[0], xs[0]
	for _, x := range xs {
		lo = math.Min(lo, x)
		hi = math.Max(hi, x)
	}
	if hi-lo < 1e-9 {
		lo, hi = lo-0.5, hi+0.5
	}
	w := (hi - lo) / float64(n)
	bins := make([]MCBin, n)
	for i := range bins {
		bins[i] = MCBin{Lo: round2(lo + float64(i)*w), Hi: round2(lo + float64(i+1)*w)}
	}
	for _, x := range xs {
		i := int((x - lo) / w)
		if i >= n {
			i = n - 1
		} else if i < 0 {
			i = 0
		}
		bins[i].Count++
	}
	return bins
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---------------------------------------------------------------------------
// Project wrapper
// ---------------------------------------------------------------------------

// BuildMonteCarloInput assembles the engine input for a project from its
// tickets, its reference set, and its config:
//   - target features: every cfp feature (ComputeCosmic) whose parent is in
//     scope (not cancelled, not backlog);
//   - platform wrap: per in-scope config/nonfunc ticket, its logged hours once
//     it's done (the estimate no longer matters), else its estimate_hours;
//     converted legacy effort and missing estimates are counted but not summed;
//   - actual hours: all hours logged on in-scope tickets.
func BuildMonteCarloInput(tickets []*ticket.Ticket, rs ReferenceSet, cfg *ticket.ProjectConfig) MCInput {
	byID := map[string]*ticket.Ticket{}
	for _, t := range tickets {
		byID[t.ID] = t
	}
	in := MCInput{
		Own:              rs.Own,
		Borrowed:         rs.Borrowed,
		Configured:       rs.Configured,
		BorrowedProjects: rs.BorrowedProjects,
		MissingProjects:  rs.MissingProjects,
		UncPct:           EffectiveCountUncPct(cfg),
		Seed:             MonteCarloSeed,
	}
	for _, f := range ComputeCosmic(tickets).Features {
		ft := byID[f.ID]
		if ft == nil || !inHoursScope(ft) {
			continue
		}
		in.Features = append(in.Features, MCFeature{
			ID: f.ID, CFP: f.CFP,
			Done:        FeatureFunctionalDone(ft, tickets),
			ActualHours: f.FunctionalHours,
		})
	}

	var etd map[string]float64
	if cfg != nil {
		etd = cfg.EffortToDays
	}
	ctx := NewEstimateContext(tickets, rs.MedianRate(), etd)
	for _, t := range tickets {
		if !inHoursScope(t) {
			continue
		}
		in.ActualHours += cosmicLoggedHours(t)
		if !ticket.IsWrapClass(ticket.ClassOf(t)) || t.Type == "meeting" || t.Type == "administration" ||
			ctx.idx.hasChildren[t.ID] {
			continue
		}
		in.Wrap.TicketCount++
		if logged := cosmicLoggedHours(t); logged > 0 && IsFunctionalDoneStatus(t.Status) {
			in.Wrap.Hours += logged
			in.Wrap.ActualCount++
			continue
		}
		// Converted legacy effort (days x 8) runs far above real wrap hours, so
		// it's flagged for review but left out of the sum, like a missing one.
		h, src := EstimatedHours(t, ctx)
		switch src {
		case SourceConverted:
			in.Wrap.ConvertedCount++
		case "":
			in.Wrap.MissingCount++
		default:
			in.Wrap.Hours += h
		}
	}
	return in
}

// EffectiveCountUncPct is the project's CFP counting uncertainty (+/- %),
// default 0.
func EffectiveCountUncPct(cfg *ticket.ProjectConfig) float64 {
	if cfg != nil && cfg.EstimateCountUncPct != nil && *cfg.EstimateCountUncPct > 0 {
		return *cfg.EstimateCountUncPct
	}
	return 0
}

// ProjectMonteCarlo runs the estimate for a project on disk: reference set from
// its config (GatherReferenceFeatures), then the engine.
func ProjectMonteCarlo(projectID, projectRoot string, tickets []*ticket.Ticket, cfg *ticket.ProjectConfig) MonteCarloResult {
	rs := GatherReferenceFeatures(projectID, projectRoot, cfg)
	return RunMonteCarlo(BuildMonteCarloInput(tickets, rs, cfg))
}
