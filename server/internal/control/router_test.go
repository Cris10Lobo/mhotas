package control

import "testing"

type recorder struct {
	name    string
	axes    []AxisID
	buttons []ButtonID
}

func (r *recorder) Name() string                       { return r.name }
func (r *recorder) SetAxis(id AxisID, _ float64) error { r.axes = append(r.axes, id); return nil }
func (r *recorder) SetButton(id ButtonID, _ bool) error {
	r.buttons = append(r.buttons, id)
	return nil
}
func (r *recorder) Ready() bool  { return true }
func (r *recorder) Close() error { return nil }

func TestRouterSendsNamedControlsToFallback(t *testing.T) {
	vj, sc := &recorder{name: "vjoy"}, &recorder{name: "simconnect"}
	r, err := NewRouter(vj, sc, []string{"rudder", "gear"})
	if err != nil {
		t.Fatal(err)
	}
	r.SetAxis(AxisPitch, 0)
	r.SetAxis(AxisRudder, 0)
	r.SetButton(ButtonGear, true)
	r.SetButton(ButtonPause, true)

	if len(vj.axes) != 1 || vj.axes[0] != AxisPitch || len(vj.buttons) != 1 || vj.buttons[0] != ButtonPause {
		t.Errorf("primary got axes %v buttons %v, want [pitch] [pause]", vj.axes, vj.buttons)
	}
	if len(sc.axes) != 1 || sc.axes[0] != AxisRudder || len(sc.buttons) != 1 || sc.buttons[0] != ButtonGear {
		t.Errorf("fallback got axes %v buttons %v, want [rudder] [gear]", sc.axes, sc.buttons)
	}
	if got := r.Route("gear"); got != "simconnect" {
		t.Errorf("Route(gear) = %q, want simconnect", got)
	}
	if got := r.Route("pitch"); got != "vjoy" {
		t.Errorf("Route(pitch) = %q, want vjoy", got)
	}
}

func TestRouterRejectsUnknownName(t *testing.T) {
	if _, err := NewRouter(&recorder{}, &recorder{}, []string{"gaer"}); err == nil {
		t.Fatal("want error for unknown control name")
	}
}
