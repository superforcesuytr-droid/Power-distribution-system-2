package model

import "testing"

func TestCapacityRounding(t *testing.T) {
	s := DefaultSettings()
	cases := map[float64]float64{63: 72, 40: 46, 32: 36, 25: 29, 200: 228, 100: 114}
	for rating, want := range cases {
		if got := s.Capacity(rating); got != want {
			t.Errorf("Capacity(%v) = %v, want %v", rating, got, want)
		}
	}
}

func TestPercentAndLevel(t *testing.T) {
	s := DefaultSettings()
	if p := s.Percent(82, 72); p != 100 {
		t.Errorf("percent should cap at 100, got %d", p)
	}
	if p := s.Percent(65, 72); p != 90 {
		t.Errorf("65/72 = %d, want 90", p)
	}
	if s.Percent(5, 0) != 0 {
		t.Error("zero capacity must give 0%")
	}
	if s.Level(90) != "critical" || s.Level(70) != "warning" || s.Level(64) != "normal" || s.Level(65) != "warning" {
		t.Error("level thresholds wrong")
	}
}

func TestCompute(t *testing.T) {
	d := BoardDetail{MCCBs: []MCCB{{RatingA: 200, MCBs: []MCB{
		{RatingA: 63, Circuits: []Circuit{{LoadA: 65, Status: StatusActive}, {LoadA: 17, Status: StatusActive}}},
		{RatingA: 40, Circuits: []Circuit{{LoadA: 12, Status: StatusActive}, {LoadA: 0, Status: StatusMaintenance}}},
	}}, {RatingA: 100, MCBs: []MCB{
		{RatingA: 32, Circuits: []Circuit{{LoadA: 40, Status: StatusActive}}},
		{RatingA: 25},
	}}}}
	d.Compute(DefaultSettings())
	if d.Totals.TotalCurrent != 134 || d.Totals.MCBCapacity != 160 || d.Totals.UtilPct != 84 || d.Totals.UtilLevel != "warning" {
		t.Errorf("totals wrong: %+v", d.Totals)
	}
	if d.Totals.ActiveCircuits != 4 || d.Totals.MaintenanceCircuits != 1 || d.Totals.CircuitCount != 5 {
		t.Errorf("counts wrong: %+v", d.Totals)
	}
	a := d.MCCBs[0]
	if a.Current != 94 || a.Capacity != 228 || a.Pct != 41 || a.MaintenanceCircuits != 1 {
		t.Errorf("MCCB-A wrong: %+v", a)
	}
	if m := a.MCBs[0]; m.Pct != 100 || m.Level != "critical" || m.Circuits[0].Pct != 90 || m.Circuits[1].Pct != 24 {
		t.Errorf("MCB-01 wrong: %+v", m)
	}
}

func TestNaturalLess(t *testing.T) {
	sorted := []string{"FAC1", "FAC 2", "FAC3", "FAC5", "FAC9", "FAC10", "FAC11", "FAC100"}
	for i := 0; i < len(sorted)-1; i++ {
		if !NaturalLess(sorted[i], sorted[i+1]) {
			t.Errorf("%q should sort before %q", sorted[i], sorted[i+1])
		}
		if NaturalLess(sorted[i+1], sorted[i]) {
			t.Errorf("%q should not sort before %q", sorted[i+1], sorted[i])
		}
	}
	if !NaturalLess("MCB-9", "MCB-10") {
		t.Error("MCB-9 should sort before MCB-10")
	}
	if !NaturalLess("MCCB-A", "MCCB-B") {
		t.Error("MCCB-A should sort before MCCB-B")
	}
	if !NaturalLess("fac1", "FAC2") {
		t.Error("comparison should ignore case")
	}
	if NaturalLess("FAC1", "FAC1") {
		t.Error("equal codes are not less than each other")
	}
	// Leading zeros must not change the value.
	if NaturalLess("MCB-010", "MCB-9") {
		t.Error("MCB-010 is ten, so it sorts after MCB-9")
	}
}
