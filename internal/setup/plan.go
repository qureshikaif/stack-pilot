package setup

type State int

const (
	StateDone    State = iota // already satisfied
	StatePending              // will be installed
	StateManual               // needs the user to do it
)

type PlanItem struct {
	Step   *Step
	State  State
	Detail string
}

// resolve orders the wanted steps after their dependencies. Dependencies are
// only followed for steps that still have work to do, so an existing Node.js
// from nvm doesn't drag in mise. Unknown IDs (e.g. macOS-only steps on Linux)
// are skipped.
func resolve(cat map[string]*Step, wanted []string, done func(*Step) (bool, string)) []PlanItem {
	var plan []PlanItem
	visited := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		s, ok := cat[id]
		if !ok || visited[id] {
			return
		}
		visited[id] = true
		if ok, detail := done(s); ok {
			plan = append(plan, PlanItem{Step: s, State: StateDone, Detail: detail})
			return
		}
		for _, n := range s.Needs {
			visit(n)
		}
		if s.automated() {
			plan = append(plan, PlanItem{Step: s, State: StatePending})
		} else {
			plan = append(plan, PlanItem{Step: s, State: StateManual, Detail: s.Manual})
		}
	}
	for _, id := range wanted {
		visit(id)
	}
	return plan
}
