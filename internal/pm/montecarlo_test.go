// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"math"
	"reflect"
	"testing"

	"hate/internal/ticket"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// mcIn is a configured input with the given features (CFP) and own rates.
func mcIn(own []float64, cfps ...int) MCInput {
	in := MCInput{Own: own, Configured: true, Seed: MonteCarloSeed}
	for i, c := range cfps {
		in.Features = append(in.Features, MCFeature{ID: string(rune('A' + i)), CFP: c})
	}
	return in
}

func TestMonteCarloDeterministic(t *testing.T) {
	in := mcIn([]float64{0.1, 0.2, 0.3, 0.5}, 10, 5, 8)
	in.Borrowed = []float64{0.15, 0.4, 0.25}
	in.UncPct = 10
	a, b := RunMonteCarlo(in), RunMonteCarlo(in)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("same input gave different results:\n%+v\n%+v", a, b)
	}
	if !a.OK || a.Runs != MonteCarloRuns || a.Seed != 42 || a.WidenK != 1.5 {
		t.Errorf("result header = ok %v runs %d seed %d k %v", a.OK, a.Runs, a.Seed, a.WidenK)
	}
}

func TestMonteCarloZeroSpread(t *testing.T) {
	in := mcIn([]float64{0.2, 0.2, 0.2}, 10, 20)
	in.Wrap = MCPlatformWrap{Hours: 4, TicketCount: 2}
	res := RunMonteCarlo(in)
	if !res.OK {
		t.Fatalf("not ok: %s", res.Error)
	}
	for _, v := range []float64{res.Code.P50, res.Code.P85, res.Code.P95} {
		if !near(v, 6, 0.01) {
			t.Errorf("code = %+v, want all 6 (30 CFP x 0.2)", res.Code)
		}
	}
	if !near(res.Total.P50, 10, 0.01) || !near(res.Total.P95, 10, 0.01) {
		t.Errorf("total = %+v, want 10 (6 code + 4 wrap)", res.Total)
	}
	if res.TotalCFP != 30 || res.FeatureCount != 2 || !near(res.RefMedianRate, 0.2, 1e-9) {
		t.Errorf("cfp=%d features=%d median=%v", res.TotalCFP, res.FeatureCount, res.RefMedianRate)
	}
}

// The K=1.5 widening shifts mu so the mean rate is unchanged.
func TestMonteCarloWideningKeepsMean(t *testing.T) {
	rates := []float64{0.1, 0.2, 0.3, 0.5, 0.25, 0.15}
	fit := fitLogNormal(rates, 1)
	mean := math.Exp(fit.mu + fit.sd*fit.sd/2)
	wide := fitLogNormal(rates, MonteCarloWidenK)
	if !near(wide.sd, 1.5*fit.sd, 1e-12) {
		t.Errorf("widened sd = %v, want %v", wide.sd, 1.5*fit.sd)
	}
	if !near(math.Exp(wide.mu+wide.sd*wide.sd/2), mean, 1e-12) {
		t.Errorf("widening moved the analytic mean rate")
	}

	cfps := []int{10, 5, 8, 12, 6, 9, 4, 7, 11, 3}
	in := mcIn(rates, cfps...)
	code, _, _ := simulateMonteCarlo(in, 1, 0, MonteCarloRuns)
	var sim float64
	for _, c := range code {
		sim += c
	}
	sim /= float64(len(code))
	total := 0
	for _, c := range cfps {
		total += c
	}
	analytic := float64(total) * mean
	if math.Abs(sim-analytic)/analytic > 0.03 {
		t.Errorf("simulated mean %v vs analytic %v (> 3%%)", sim, analytic)
	}
	// Sanity: the analytic mean is above the median rate (log-normal skew),
	// so a mean-preserving widening keeps the center rather than the median.
	if res := RunMonteCarlo(in); !res.OK || res.Code.P50 >= analytic {
		t.Errorf("P50 %v should sit below the mean %v", res.Code.P50, analytic)
	}
}

func TestOwnBlendProbability(t *testing.T) {
	for _, c := range []struct {
		n    int
		want float64
	}{{0, 0}, {5, 0.5}, {15, 1}, {10, 10.0 / 15}, {20, 1}} {
		if got := OwnBlendProbability(c.n); !near(got, c.want, 1e-12) {
			t.Errorf("OwnBlendProbability(%d) = %v, want %v", c.n, got, c.want)
		}
	}
	rep := func(n int) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = 0.2
		}
		return out
	}
	borrowed := rep(5)
	for _, c := range []struct {
		own  int
		want float64
	}{{0, 0}, {5, 0.5}, {15, 1}} {
		in := mcIn(rep(c.own), 10)
		in.Borrowed = borrowed
		if res := RunMonteCarlo(in); !res.OK || res.POwn != c.want || res.NOwn != c.own || res.NBorrowed != 5 {
			t.Errorf("own %d: ok=%v p_own=%v n_own=%d n_borrowed=%d, want p_own %v", c.own, res.OK, res.POwn, res.NOwn, res.NBorrowed, c.want)
		}
	}
	// Only own features: they're the whole pool.
	if res := RunMonteCarlo(mcIn(rep(3), 10)); res.POwn != 1 {
		t.Errorf("own-only p_own = %v, want 1", res.POwn)
	}
}

// With own_N = 5 and two pools at very different rates, about half the draws
// come from each.
func TestMonteCarloBlendMix(t *testing.T) {
	in := mcIn([]float64{1, 1, 1, 1, 1}, 1)
	in.Borrowed = []float64{0.1, 0.1, 0.1}
	res := RunMonteCarlo(in)
	// Half the runs draw 1h, half 0.1h: P50 sits at one of the two, P85 at 1.
	if res.Code.P85 != 1 || res.Code.P95 != 1 {
		t.Errorf("code = %+v, want P85 = P95 = 1", res.Code)
	}
	low := 0
	for _, b := range res.Histogram {
		if b.Hi <= 0.55 {
			low += b.Count
		}
	}
	if low < 4700 || low > 5300 {
		t.Errorf("borrowed draws = %d of 10000, want about 5000", low)
	}
}

func TestMonteCarloErrorStates(t *testing.T) {
	in := mcIn([]float64{0.2, 0.3}, 10)
	if res := RunMonteCarlo(in); res.OK || res.Error != MCErrTooFewRefs || res.NOwn != 2 {
		t.Errorf("2 refs: ok=%v err=%q n_own=%d", res.OK, res.Error, res.NOwn)
	}
	in = mcIn([]float64{0.2, 0.3, 0.4}, 10)
	in.Configured = false
	if res := RunMonteCarlo(in); res.OK || res.Error != MCErrNoReference {
		t.Errorf("unconfigured: ok=%v err=%q", res.OK, res.Error)
	}
	in = mcIn([]float64{0.2, 0.3, 0.4})
	in.Wrap = MCPlatformWrap{Hours: 3, TicketCount: 1}
	in.ActualHours = 7
	res := RunMonteCarlo(in)
	if res.OK || res.Error != MCErrNoFeatures {
		t.Errorf("no features: ok=%v err=%q", res.OK, res.Error)
	}
	// Error states still carry the context the panel shows, and empty slices.
	if res.PlatformWrap.Hours != 3 || res.ActualHours != 7 || res.Histogram == nil || res.BorrowedProjects == nil {
		t.Errorf("error state context = %+v", res)
	}
}

func TestMonteCarloCountingUncertaintyWidens(t *testing.T) {
	in := mcIn([]float64{0.2, 0.2, 0.2}, 10, 20)
	in.UncPct = 20
	res := RunMonteCarlo(in)
	if !near(res.Code.P50, 6, 0.1) {
		t.Errorf("P50 = %v, want about 6", res.Code.P50)
	}
	// U(0.8, 1.2) x 6: P95 = 6 x 1.18 = 7.08.
	if !near(res.Code.P95, 7.08, 0.05) {
		t.Errorf("P95 = %v, want about 7.08", res.Code.P95)
	}

	spread := func(u float64) float64 {
		in := mcIn([]float64{0.1, 0.2, 0.3, 0.5}, 10, 5, 8)
		in.UncPct = u
		r := RunMonteCarlo(in)
		return r.Code.P95 - r.Code.P50
	}
	if a, b := spread(0), spread(30); b <= a {
		t.Errorf("uncertainty 30%% spread %v <= no-uncertainty spread %v", b, a)
	}
}

func TestMonteCarloHistogram(t *testing.T) {
	in := mcIn([]float64{0.1, 0.2, 0.3, 0.5}, 10, 5, 8)
	in.Wrap.Hours = 2
	res := RunMonteCarlo(in)
	if len(res.Histogram) != 20 {
		t.Fatalf("bins = %d, want 20", len(res.Histogram))
	}
	n := 0
	for _, b := range res.Histogram {
		n += b.Count
		if b.Hi < b.Lo {
			t.Errorf("bin %+v inverted", b)
		}
	}
	if n != MonteCarloRuns {
		t.Errorf("histogram counts sum to %d, want %d", n, MonteCarloRuns)
	}
	// All-equal totals still give 20 bins summing to runs.
	res = RunMonteCarlo(mcIn([]float64{0.2, 0.2, 0.2}, 10))
	n = 0
	for _, b := range res.Histogram {
		n += b.Count
	}
	if len(res.Histogram) != 20 || n != MonteCarloRuns {
		t.Errorf("flat histogram: %d bins, %d counts", len(res.Histogram), n)
	}
}

func TestMonteCarloProjectedFinish(t *testing.T) {
	in := mcIn([]float64{0.2, 0.2, 0.2})
	in.Features = []MCFeature{
		{ID: "A", CFP: 10, Done: true, ActualHours: 5}, // simulated 2h, actually took 5
		{ID: "B", CFP: 10}, // 2h to go
		{ID: "C", CFP: 5},  // 1h to go
	}
	res := RunMonteCarlo(in)
	pf := res.ProjectedFinish
	if pf.DoneActualHours != 5 || pf.RemainingFeatureCount != 2 || !near(pf.P50, 8, 0.01) || !near(pf.P85, 8, 0.01) {
		t.Errorf("projected finish = %+v, want done 5, remaining 2, P50 = P85 = 8", pf)
	}
	if !near(res.Code.P50, 5, 0.01) {
		t.Errorf("code P50 = %v, want 5 (25 CFP x 0.2)", res.Code.P50)
	}
}

// BuildMonteCarloInput picks in-scope features, sums platform wrap, and totals
// actual hours.
func TestBuildMonteCarloInput(t *testing.T) {
	reason := "dropped"
	m := "m"
	var tickets []*ticket.Ticket
	tickets = append(tickets, refFeature("A", 10, 2, 1, "complete")...)   // done, 2h functional
	tickets = append(tickets, refFeature("B", 6, 1, 3, "in_progress")...) // not done
	tickets = append(tickets, refFeature("X", 8, 1, 4, "in_progress")...) // cancelled parent
	for _, x := range tickets[len(tickets)-2:] {                          // cancel the feature and its child
		x.Status = "closed"
		x.CancellationReason = &reason
	}
	tickets = append(tickets, refFeature("Y", 4, 0, 0, "")...) // backlog parent
	tickets[len(tickets)-1].Tags = append(tickets[len(tickets)-1].Tags, ticket.BacklogTag)
	tickets = append(tickets,
		&ticket.Ticket{ID: "w1", Tags: []string{"parent:A", ticket.ClassConfig}, EstimateHours: f64(2), TimeEntries: te(1)},
		&ticket.Ticket{ID: "w2", Tags: []string{ticket.ClassNonfunc}, Effort: &m},
		&ticket.Ticket{ID: "w3", Tags: []string{ticket.ClassNonfunc}},
		&ticket.Ticket{ID: "w4", Tags: []string{ticket.ClassConfig, ticket.BacklogTag}, EstimateHours: f64(8)},
		&ticket.Ticket{ID: "w5", Status: "complete", Tags: []string{ticket.ClassConfig}, Effort: &m, TimeEntries: te(1.5)}, // done: actual, not converted
		&ticket.Ticket{ID: "mt", Type: "meeting", TimeEntries: te(0.5)},
	)
	unc := 15.0
	rs := ReferenceSet{Configured: true, Own: []float64{0.2}, NOwn: 1}
	in := BuildMonteCarloInput(tickets, rs, &ticket.ProjectConfig{EstimateCountUncPct: &unc})

	if len(in.Features) != 2 {
		t.Fatalf("features = %+v, want A and B", in.Features)
	}
	byID := map[string]MCFeature{}
	for _, f := range in.Features {
		byID[f.ID] = f
	}
	if a := byID["A"]; !a.Done || a.CFP != 10 || a.ActualHours != 2 {
		t.Errorf("A = %+v, want done, 10 CFP, 2h", a)
	}
	if b := byID["B"]; b.Done || b.CFP != 6 {
		t.Errorf("B = %+v, want not done, 6 CFP", b)
	}
	w := in.Wrap
	if w.TicketCount != 4 || w.ActualCount != 1 || w.ConvertedCount != 1 || w.MissingCount != 1 || !near(w.Hours, 2+1.5, 1e-9) {
		t.Errorf("wrap = %+v, want 4 tickets, 1 actual, 1 converted, 1 missing, %v h", w, 2+1.5)
	}
	// A 2h + B 3h + w1 1h + w5 1.5h + meeting 0.5h; the cancelled ticket's 4h is out.
	if !near(in.ActualHours, 8, 1e-9) {
		t.Errorf("actual hours = %v, want 8", in.ActualHours)
	}
	if in.UncPct != 15 || in.Seed != MonteCarloSeed || !in.Configured {
		t.Errorf("unc=%v seed=%d configured=%v", in.UncPct, in.Seed, in.Configured)
	}
}

func TestComputeCosmicCalibrationSlice(t *testing.T) {
	var tickets []*ticket.Ticket
	tickets = append(tickets, refFeature("S1", 5, 1, 1, "complete")...)
	tickets = append(tickets, refFeature("S2", 5, 1, 1, "in_progress")...)
	tickets = append(tickets, refFeature("N", 5, 1, 1, "complete")...)
	tickets[0].Tags = append(tickets[0].Tags, CalibrationSliceTag)
	tickets[2].Tags = append(tickets[2].Tags, CalibrationSliceTag)
	rep := ComputeCosmic(tickets)
	if rep.SliceTotal != 2 || rep.SliceDone != 1 {
		t.Errorf("slice = %d/%d, want 1 done of 2", rep.SliceDone, rep.SliceTotal)
	}
	for _, f := range rep.Features {
		if f.CalibrationSlice != (f.ID != "N") {
			t.Errorf("feature %s calibration_slice = %v", f.ID, f.CalibrationSlice)
		}
	}
}

// ---------------------------------------------------------------------------
// Manual baseline
// ---------------------------------------------------------------------------

func TestFitManualBaseline(t *testing.T) {
	m := ticket.ManualBaseline{Low: 0.08, Likely: 0.25, High: 1.0}
	fit := fitManualBaseline(m)
	wantSD := ((math.Log(0.25) - math.Log(0.08)) + (math.Log(1.0) - math.Log(0.25))) / 2 / 1.2816
	if !near(fit.mu, math.Log(0.25), 1e-12) || !near(fit.sd, wantSD, 1e-12) {
		t.Errorf("fit = %+v, want mu ln(0.25), sd %v", fit, wantSD)
	}
	if fit := fitManualBaseline(ticket.ManualBaseline{Low: 2, Likely: 2, High: 2}); !near(fit.mu, math.Log(2), 1e-12) || fit.sd != 0 {
		t.Errorf("flat fit = %+v, want mu ln 2, sd 0", fit)
	}
}

func TestManualPresetsAndValidation(t *testing.T) {
	if len(ManualPresets) != 2 || ManualPresets[0].ID != "agentic" || ManualPresets[1].ID != "traditional" {
		t.Fatalf("presets = %+v", ManualPresets)
	}
	if d := DefaultManualBaseline(); d != (ticket.ManualBaseline{Low: 0.08, Likely: 0.25, High: 1}) {
		t.Errorf("default baseline = %+v, want agentic", d)
	}
	for m, want := range map[ticket.ManualBaseline]string{
		{Low: 0.08, Likely: 0.25, High: 1}: "agentic",
		{Low: 8, Likely: 12, High: 18}:     "traditional",
		{Low: 8, Likely: 13, High: 18}:     "custom",
	} {
		if got := ManualPresetID(m); got != want {
			t.Errorf("ManualPresetID(%+v) = %q, want %q", m, got, want)
		}
	}
	for m, want := range map[ticket.ManualBaseline]bool{
		{Low: 0.1, Likely: 0.2, High: 1}: true,
		{Low: 1, Likely: 1, High: 1}:     true,
		{Low: 0.3, Likely: 0.2, High: 1}: false,
		{Low: 0.1, Likely: 2, High: 1}:   false,
		{Low: 0, Likely: 0.2, High: 1}:   false,
	} {
		if got := ValidManualBaseline(m); got != want {
			t.Errorf("ValidManualBaseline(%+v) = %v, want %v", m, got, want)
		}
	}
}

func TestEffectiveEstimateRefs(t *testing.T) {
	// No saved inputs: manual (agentic) + own, flagged as defaults.
	for _, cfg := range []*ticket.ProjectConfig{nil, {}} {
		r := EffectiveEstimateRefs(cfg)
		if !r.Manual || !r.Own || r.All || len(r.Projects) != 0 || !r.DefaultsApplied || r.ManualBaseline != DefaultManualBaseline() {
			t.Errorf("no saved inputs: %+v", r)
		}
	}
	// Saved values are used exactly, including an explicit untick of everything.
	off, on := false, true
	if r := EffectiveEstimateRefs(&ticket.ProjectConfig{EstimateRefManual: &off}); r.Manual || r.Own || r.DefaultsApplied {
		t.Errorf("explicit untick: %+v", r)
	}
	trad := ticket.ManualBaseline{Low: 8, Likely: 12, High: 18}
	if r := EffectiveEstimateRefs(&ticket.ProjectConfig{EstimateRefManual: &on, EstimateManual: &trad}); !r.Manual || r.Own || r.ManualBaseline != trad {
		t.Errorf("saved manual: %+v", r)
	}
	// ref_own saved without the manual fields (an older config): no manual.
	if r := EffectiveEstimateRefs(&ticket.ProjectConfig{EstimateRefOwn: true}); r.Manual || !r.Own || r.DefaultsApplied {
		t.Errorf("own only: %+v", r)
	}
}

// Manual baseline only, 20 CFP feature, likely 0.25: P50 about 5h; no
// reference features needed.
func TestMonteCarloManualOnly(t *testing.T) {
	m := DefaultManualBaseline()
	in := mcIn(nil, 20)
	in.Manual = &m
	res := RunMonteCarlo(in)
	if !res.OK {
		t.Fatalf("not ok: %s", res.Error)
	}
	if !near(res.Code.P50, 5, 0.15) {
		t.Errorf("code P50 = %v, want about 5 (20 x 0.25)", res.Code.P50)
	}
	if res.POwn != 0 || res.PManual != 1 || !res.ManualInUse || res.RefMedianRate != 0.25 {
		t.Errorf("p_own=%v p_manual=%v in_use=%v median=%v", res.POwn, res.PManual, res.ManualInUse, res.RefMedianRate)
	}
	// P90 of the draws is about High (20 x 1.0 = 20h): no widening applied.
	if res.Code.P85 >= 20 || res.Code.P95 <= 20 {
		t.Errorf("code = %+v, want P85 < 20 < P95 (P90 = 20)", res.Code)
	}
	// A flat range gives a flat result.
	flat := ticket.ManualBaseline{Low: 0.5, Likely: 0.5, High: 0.5}
	in.Manual = &flat
	if res := RunMonteCarlo(in); !near(res.Code.P50, 10, 0.01) || !near(res.Code.P95, 10, 0.01) {
		t.Errorf("flat manual code = %+v, want 10", res.Code)
	}
}

// Manual baseline + 15 finished own features: the baseline no longer affects
// the draw.
func TestMonteCarloManualHandsOverAt15(t *testing.T) {
	own := make([]float64, 15)
	for i := range own {
		own[i] = 0.1 + 0.02*float64(i)
	}
	base := mcIn(own, 10, 20)
	withManual := base
	m := ticket.ManualBaseline{Low: 8, Likely: 12, High: 18}
	withManual.Manual = &m
	a, b := RunMonteCarlo(base), RunMonteCarlo(withManual)
	if b.POwn != 1 || b.PManual != 0 || b.ManualInUse {
		t.Errorf("p_own=%v p_manual=%v in_use=%v, want 1/0/false", b.POwn, b.PManual, b.ManualInUse)
	}
	if !reflect.DeepEqual(a.Code, b.Code) || !reflect.DeepEqual(a.Total, b.Total) {
		t.Errorf("manual changed the result: %+v vs %+v", a.Code, b.Code)
	}
}

// Manual baseline + 5 finished own features: about half the draws are own.
func TestMonteCarloManualBlendWithOwn(t *testing.T) {
	in := mcIn([]float64{1, 1, 1, 1, 1}, 1)
	m := ticket.ManualBaseline{Low: 0.1, Likely: 0.1, High: 0.1}
	in.Manual = &m
	res := RunMonteCarlo(in)
	if res.POwn != 0.5 || res.PManual != 0.5 || !res.ManualInUse {
		t.Errorf("p_own=%v p_manual=%v in_use=%v, want 0.5/0.5/true", res.POwn, res.PManual, res.ManualInUse)
	}
	low := 0
	for _, b := range res.Histogram {
		if b.Hi <= 0.55 {
			low += b.Count
		}
	}
	if low < 4700 || low > 5300 {
		t.Errorf("manual draws = %d of 10000, want about 5000", low)
	}
}

// Own, borrowed and manual together: own by p_own, the rest split borrowed :
// manual as N_borrowed : 5.
func TestMonteCarloManualBlendWithBorrowed(t *testing.T) {
	in := mcIn([]float64{1, 1, 1, 1, 1}, 1)                   // own 1 h/CFP, p_own 0.5
	in.Borrowed = []float64{0.1, 0.1, 0.1, 0.1, 0.1}          // borrowed 0.1, 5 features
	m := ticket.ManualBaseline{Low: 10, Likely: 10, High: 10} // manual 10, weight 5
	in.Manual = &m
	res := RunMonteCarlo(in)
	if res.POwn != 0.5 || res.PManual != 0.25 {
		t.Errorf("p_own=%v p_manual=%v, want 0.5/0.25", res.POwn, res.PManual)
	}
	var lo, hi int
	for _, b := range res.Histogram {
		if b.Hi <= 0.6 {
			lo += b.Count
		} else if b.Lo >= 9 {
			hi += b.Count
		}
	}
	if lo < 2300 || lo > 2700 || hi < 2300 || hi > 2700 {
		t.Errorf("borrowed draws %d, manual draws %d of 10000, want about 2500 each", lo, hi)
	}
	// No own features: borrowed vs manual only.
	in.Own = nil
	if res := RunMonteCarlo(in); res.POwn != 0 || res.PManual != 0.5 {
		t.Errorf("borrowed + manual: p_own=%v p_manual=%v, want 0/0.5", res.POwn, res.PManual)
	}
}

// Without the manual baseline nothing changes: the 3-feature minimum holds and
// p_manual is 0.
func TestMonteCarloNoManualUnchanged(t *testing.T) {
	if res := RunMonteCarlo(mcIn([]float64{0.2, 0.3}, 10)); res.Error != MCErrTooFewRefs || res.PManual != 0 || res.ManualInUse {
		t.Errorf("2 refs, no manual: %+v", res)
	}
	m := DefaultManualBaseline()
	in := mcIn([]float64{0.2, 0.3}, 10)
	in.Manual = &m
	if res := RunMonteCarlo(in); !res.OK || res.POwn != 0.2857 { // 2 / (2 + 5)
		t.Errorf("2 refs + manual: ok=%v err=%q p_own=%v", res.OK, res.Error, res.POwn)
	}
	rs := ReferenceSet{Configured: true, Manual: &m}
	if got := BuildMonteCarloInput(nil, rs, nil); got.Manual == nil || *got.Manual != m {
		t.Errorf("BuildMonteCarloInput dropped the manual baseline")
	}
}
