package main

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

//
// ------------------------ Modèle SMA ------------------------
//

type DroneMode string

const (
	ModeSearching  DroneMode = "searching"
	ModeResponding DroneMode = "responding"
	ModeHovering   DroneMode = "hovering"
)

type Drone struct {
	ID        int       `json:"id"`
	X         float64   `json:"x"`
	Y         float64   `json:"y"`
	Vx        float64   `json:"vx"`
	Vy        float64   `json:"vy"`
	Mode      DroneMode `json:"state"`
	TargetX   float64   `json:"targetX"`
	TargetY   float64   `json:"targetY"`
	HasTarget bool      `json:"hasTarget"`
	FoundID   int       `json:"foundID"`

	RespondTimer float64 `json:"respondTimer"`
}

type Survivor struct {
	ID     int     `json:"id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Saved  bool    `json:"saved"`
	Radius float64 `json:"radius"`
}

type Trace struct {
	ID       int     `json:"id"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Radius   float64 `json:"radius"`
	Consumed bool    `json:"consumed"`
}

type SimConfig struct {
	Width            float64 `json:"width"`
	Height           float64 `json:"height"`
	NumDrones        int     `json:"numDrones"`
	NumSurvivors     int     `json:"numSurvivors"`
	NumTraces        int     `json:"numTraces"`
	DroneSpeed       float64 `json:"droneSpeed"`
	DetectionRadius  float64 `json:"detectionRadius"`
	CommRadius       float64 `json:"commRadius"`
	MaxHelpersPerHit int     `json:"maxHelpersPerHit"`
	TimeStep         float64 `json:"timeStep"`
}

// Statistiques globales de la simulation
type SimStats struct {
	TotalTime      float64 `json:"totalTime"`
	TotalSurvivors int     `json:"totalSurvivors"`
	SavedSurvivors int     `json:"savedSurvivors"`
	Drones         int     `json:"drones"`
	Traces         int     `json:"traces"`
	TracesConsumed int     `json:"tracesConsumed"`
	Finished       bool    `json:"finished"`
}

type Environment struct {
	Config    SimConfig  `json:"config"`
	Drones    []Drone    `json:"drones"`
	Survivors []Survivor `json:"survivors"`
	Traces    []Trace    `json:"traces"`
	Time      float64    `json:"time"`
	Finished  bool       `json:"finished"`
	Stats     SimStats   `json:"stats"`
}

// Interface agent
type Agent interface {
	ID() int
	Percept(env *Environment)
	Deliberate()
	Act(env *Environment)
	Start()
}

// ---------------------- Agent Drone ------------------------

type DroneAgent struct {
	index         int
	cfg           *SimConfig
	lastPerceived *Environment
}

func NewDroneAgent(index int, cfg *SimConfig) *DroneAgent {
	return &DroneAgent{
		index: index,
		cfg:   cfg,
	}
}

func (d *DroneAgent) ID() int { return d.index }

func (d *DroneAgent) Start() {}

func (d *DroneAgent) Percept(env *Environment) {
	d.lastPerceived = env
}

func (d *DroneAgent) Deliberate() {}

func (d *DroneAgent) Act(env *Environment) {
	if env == nil || d.lastPerceived == nil {
		return
	}
	cfg := env.Config
	dr := &env.Drones[d.index]

	// Timeout renfort pour éviter les blocages
	const respondTimeout = 8.0
	if dr.Mode == ModeResponding {
		dr.RespondTimer += cfg.TimeStep
		if dr.RespondTimer > respondTimeout {
			dr.Mode = ModeSearching
			dr.HasTarget = false
			dr.Vx, dr.Vy = 0, 0
		}
	} else {
		dr.RespondTimer = 0
	}

	// Mode Hovering : ne bouge plus
	if dr.Mode == ModeHovering {
		dr.Vx, dr.Vy = 0, 0
		return
	}

	// 1) Mouvement
	switch dr.Mode {
	case ModeSearching:
		if rand.Float64() < 0.02 {
			angle := rand.Float64() * 2 * math.Pi
			dr.Vx = math.Cos(angle) * cfg.DroneSpeed
			dr.Vy = math.Sin(angle) * cfg.DroneSpeed
		}
	case ModeResponding:
		if dr.HasTarget {
			dx := dr.TargetX - dr.X
			dy := dr.TargetY - dr.Y
			dist := math.Hypot(dx, dy)
			if dist > 1 {
				dr.Vx = dx / dist * cfg.DroneSpeed
				dr.Vy = dy / dist * cfg.DroneSpeed
			} else {
				dr.Mode = ModeSearching
				dr.HasTarget = false
			}
		}
	}

	// Mise à jour position
	dt := cfg.TimeStep
	dr.X += dr.Vx * dt
	dr.Y += dr.Vy * dt

	// Bords
	if dr.X < 0 {
		dr.X = 0
		dr.Vx = -dr.Vx
	}
	if dr.X > cfg.Width {
		dr.X = cfg.Width
		dr.Vx = -dr.Vx
	}
	if dr.Y < 0 {
		dr.Y = 0
		dr.Vy = -dr.Vy
	}
	if dr.Y > cfg.Height {
		dr.Y = cfg.Height
		dr.Vy = -dr.Vy
	}

	// 2) Traces
	for ti := range env.Traces {
		tr := &env.Traces[ti]
		if tr.Consumed {
			continue
		}
		if distance(dr.X, dr.Y, tr.X, tr.Y) <= cfg.DetectionRadius+tr.Radius {
			tr.Consumed = true
			callNeighborsForHelp(env, d.index, ti)
		}
	}

	// 3) Survivants
	for si := range env.Survivors {
		s := &env.Survivors[si]
		if s.Saved {
			continue
		}
		if distance(dr.X, dr.Y, s.X, s.Y) <= cfg.DetectionRadius+s.Radius {
			dr.Mode = ModeHovering
			dr.X = s.X
			dr.Y = s.Y
			dr.Vx, dr.Vy = 0, 0
			dr.HasTarget = false
			dr.FoundID = s.ID
			s.Saved = true
			break
		}
	}
}

func callNeighborsForHelp(env *Environment, droneIndex, traceIndex int) {
	cfg := env.Config
	source := env.Drones[droneIndex]
	trace := env.Traces[traceIndex]

	type candidate struct {
		idx  int
		dist float64
	}

	var neighbors []candidate
	for i := range env.Drones {
		if i == droneIndex {
			continue
		}
		d := &env.Drones[i]
		if d.Mode != ModeSearching {
			continue
		}
		dist := distance(source.X, source.Y, d.X, d.Y)
		if dist <= cfg.CommRadius {
			neighbors = append(neighbors, candidate{idx: i, dist: dist})
		}
	}
	if len(neighbors) > cfg.MaxHelpersPerHit {
		neighbors = neighbors[:cfg.MaxHelpersPerHit]
	}
	for _, n := range neighbors {
		d := &env.Drones[n.idx]
		d.Mode = ModeResponding
		d.TargetX = trace.X
		d.TargetY = trace.Y
		d.HasTarget = true
	}
}

//
// ------------------------ Simulation ------------------------
//

type Simulation struct {
	mu      sync.RWMutex
	env     Environment
	agents  []Agent
	running bool
}

func defaultConfig() SimConfig {
	return SimConfig{
		Width:            1000,
		Height:           700,
		NumDrones:        20,
		NumSurvivors:     5,
		NumTraces:        8,
		DroneSpeed:       50,
		DetectionRadius:  40,
		CommRadius:       150,
		MaxHelpersPerHit: 3,
		TimeStep:         0.1,
	}
}

func NewSimulation(cfg SimConfig) *Simulation {
	s := &Simulation{}
	s.Reset(cfg)
	return s
}

func (s *Simulation) Reset(cfg SimConfig) {
	if cfg.Width <= 0 || cfg.Height <= 0 {
		cfg.Width, cfg.Height = 1000, 700
	}
	if cfg.NumDrones <= 0 {
		cfg.NumDrones = 10
	}
	if cfg.DroneSpeed <= 0 {
		cfg.DroneSpeed = 50
	}
	if cfg.DetectionRadius <= 0 {
		cfg.DetectionRadius = 40
	}
	if cfg.CommRadius <= 0 {
		cfg.CommRadius = 150
	}
	if cfg.MaxHelpersPerHit <= 0 {
		cfg.MaxHelpersPerHit = 3
	}
	if cfg.TimeStep <= 0 {
		cfg.TimeStep = 0.1
	}

	rand.Seed(time.Now().UnixNano())

	centerX := cfg.Width / 2
	centerY := cfg.Height / 2

	drones := make([]Drone, cfg.NumDrones)
	agents := make([]Agent, cfg.NumDrones)
	for i := range drones {
		angle := rand.Float64() * 2 * math.Pi
		speed := cfg.DroneSpeed
		drones[i] = Drone{
			ID:           i,
			X:            centerX,
			Y:            centerY,
			Vx:           math.Cos(angle) * speed,
			Vy:           math.Sin(angle) * speed,
			Mode:         ModeSearching,
			TargetX:      0,
			TargetY:      0,
			HasTarget:    false,
			FoundID:      -1,
			RespondTimer: 0,
		}
		agents[i] = NewDroneAgent(i, &cfg)
	}

	survivors := make([]Survivor, cfg.NumSurvivors)
	for i := range survivors {
		survivors[i] = Survivor{
			ID:     i,
			X:      rand.Float64() * cfg.Width,
			Y:      rand.Float64() * cfg.Height,
			Saved:  false,
			Radius: 6, // plus petit
		}
	}

	traces := make([]Trace, cfg.NumTraces)
	if len(survivors) > 0 {
		for i := range traces {
			sv := &survivors[i%len(survivors)]

			// Rayon de la trace : plus grand qu'avant
			traceRadius := cfg.DetectionRadius * 1.5

			// On veut que le survivant soit À L’INTÉRIEUR du cercle
			// donc la distance centreTrace–survivant <= traceRadius - marge
			margin := sv.Radius + 3
			rMax := traceRadius - margin
			if rMax < 0 {
				rMax = 0
			}

			// On choisit rho dans [0, rMax]
			// (option simple ; si tu veux uniforme en surface,
			// tu peux faire rho := math.Sqrt(rand.Float64()) * rMax)
			rho := rand.Float64() * rMax
			theta := rand.Float64() * 2 * math.Pi

			x := sv.X + rho*math.Cos(theta)
			y := sv.Y + rho*math.Sin(theta)

			// clamp dans la map
			if x < 0 {
				x = 0
			}
			if x > cfg.Width {
				x = cfg.Width
			}
			if y < 0 {
				y = 0
			}
			if y > cfg.Height {
				y = cfg.Height
			}

			traces[i] = Trace{
				ID:       i,
				X:        x,
				Y:        y,
				Radius:   traceRadius,
				Consumed: false,
			}
		}
	} else {
		// fallback si pas de survivants
		for i := range traces {
			traces[i] = Trace{
				ID:       i,
				X:        rand.Float64() * cfg.Width,
				Y:        rand.Float64() * cfg.Height,
				Radius:   cfg.DetectionRadius * 1.5, // même rayon augmenté
				Consumed: false,
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.env = Environment{
		Config:    cfg,
		Drones:    drones,
		Survivors: survivors,
		Traces:    traces,
		Time:      0,
		Finished:  false,
		Stats:     SimStats{},
	}
	s.agents = agents
	s.running = true
}

func (s *Simulation) step() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.env.Finished {
		return
	}

	for _, ag := range s.agents {
		ag.Percept(&s.env)
		ag.Deliberate()
		ag.Act(&s.env)
	}

	s.env.Time += s.env.Config.TimeStep

	// Vérifier si tous les survivants sont sauvés
	allSaved := true
	savedCount := 0
	for i := range s.env.Survivors {
		if s.env.Survivors[i].Saved {
			savedCount++
		} else {
			allSaved = false
		}
	}

	if allSaved {
		// calcul des stats finales
		tracesConsumed := 0
		for i := range s.env.Traces {
			if s.env.Traces[i].Consumed {
				tracesConsumed++
			}
		}
		stats := SimStats{
			TotalTime:      s.env.Time,
			TotalSurvivors: len(s.env.Survivors),
			SavedSurvivors: savedCount,
			Drones:         len(s.env.Drones),
			Traces:         len(s.env.Traces),
			TracesConsumed: tracesConsumed,
			Finished:       true,
		}
		s.env.Stats = stats
		s.env.Finished = true
		s.running = false
	}
}

func (s *Simulation) Run(ctx context.Context) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			running := s.running
			s.mu.RUnlock()
			if running {
				s.step()
			}
		}
	}
}

func (s *Simulation) Snapshot() Environment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.env
}

func (s *Simulation) SetRunning(r bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = r
}

func (s *Simulation) ToggleRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// si déjà fini, on ne relance pas
	if s.env.Finished {
		return s.running
	}
	s.running = !s.running
	return s.running
}

//
// ------------------------ HTTP ------------------------
//

var sim *Simulation

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleGetState(w http.ResponseWriter, r *http.Request) {
	env := sim.Snapshot()
	writeJSON(w, env)
}

func handleReset(w http.ResponseWriter, r *http.Request) {
	var cfg SimConfig
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			cfg = defaultConfig()
		}
	} else {
		cfg = defaultConfig()
	}
	sim.Reset(cfg)
	env := sim.Snapshot()
	writeJSON(w, env)
}

func handleToggle(w http.ResponseWriter, r *http.Request) {
	running := sim.ToggleRunning()
	writeJSON(w, map[string]any{
		"running": running,
	})
}

func main() {
	cfg := defaultConfig()
	sim = NewSimulation(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sim.Run(ctx)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/state", handleGetState)
	mux.HandleFunc("/api/reset", handleReset)
	mux.HandleFunc("/api/toggle", handleToggle)

	webDir := "web"
	fs := http.FileServer(http.Dir(webDir))

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFile(w, r, filepath.Join(webDir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	}))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Server listening on http://localhost:%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func distance(x1, y1, x2, y2 float64) float64 {
	return math.Hypot(x1-x2, y1-y2)
}
