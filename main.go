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
	ModeReturning  DroneMode = "returning"
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

	// per-drone parameters
	Speed             float64 `json:"speed"`
	Weight            float64 `json:"weight"`
	Autonomy          float64 `json:"autonomy"`
	RemainingAutonomy float64 `json:"remainingAutonomy"`
	DetectionRadius   float64 `json:"detectionRadius"`
}

type Survivor struct {
	ID     int     `json:"id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Saved  bool    `json:"saved"`
	Radius float64 `json:"radius"`
}

type Trace struct {
	ID         int     `json:"id"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Radius     float64 `json:"radius"`
	Consumed   bool    `json:"consumed"`
	SurvivorID int     `json:"survivorId"` // -1 si aucune
	Activated  bool    `json:"-"`          // interne : renfort déjà appelé ou pas
}

type ChargingPoint struct {
	ID int     `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

// type of drones, loaded from JSON
type DroneType struct {
	Name            string  `json:"name"`
	Count           int     `json:"count"`
	Speed           float64 `json:"speed"`
	Weight          float64 `json:"weight"`
	Autonomy        float64 `json:"autonomy"`        // total range
	DetectionRadius float64 `json:"detectionRadius"` // per-type detection radius
}

type SimConfig struct {
	Width            float64         `json:"width"`
	Height           float64         `json:"height"`
	NumDrones        int             `json:"numDrones"`
	NumSurvivors     int             `json:"numSurvivors"`
	NumTraces        int             `json:"numTraces"`
	DroneSpeed       float64         `json:"droneSpeed"`      // default speed if no per-type speed
	DetectionRadius  float64         `json:"detectionRadius"` // default per-drone detection radius
	rayonAide        float64         `json:"rayonAide"`
	MaxHelpersPerHit int             `json:"maxHelpersPerHit"`
	TimeStep         float64         `json:"timeStep"`
	DroneTypes       []DroneType     `json:"droneTypes"` // heterogeneous drone types
	ChargingPoints   []ChargingPoint `json:"chargingPoints"`
	BaseX            float64         `json:"baseX"` // base position X
	BaseY            float64         `json:"baseY"` // base position Y

	// paramètres entraînables
	tailleIndice    float64 `json:"tailleIndice"`
	tauxExploration float64 `json:"tauxExploration"`
	dureeEngagement float64 `json:"dureeEngagement"`
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
	Config         SimConfig       `json:"config"`
	Drones         []Drone         `json:"drones"`
	Survivors      []Survivor      `json:"survivors"`
	Traces         []Trace         `json:"traces"`
	ChargingPoints []ChargingPoint `json:"chargingPoints"`
	Time           float64         `json:"time"`
	Finished       bool            `json:"finished"`
	Stats          SimStats        `json:"stats"`
	Heatmap        [][]float64     `json:"heatmap"`
}

// Interface agent
type Agent interface {
	ID() int
	Percept(env *Environment)
	Deliberate()
	Act(env *Environment)
	Start()
}

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

	// Rayon de zone de recherche autour d'une trace
	zoneRadius := cfg.DetectionRadius * cfg.tailleIndice
	if zoneRadius <= 0 {
		zoneRadius = cfg.DetectionRadius
		if zoneRadius <= 0 {
			zoneRadius = 80 // fallback
		}
	}

	// Si le drone est assigné à une zone (HasTarget = true),
	// on vérifie s'il reste un survivant non sauvé dans cette zone.
	if dr.HasTarget {
		aliveInZone := false
		for i := range env.Survivors {
			s := &env.Survivors[i]
			if s.Saved {
				continue
			}
			if distance(s.X, s.Y, dr.TargetX, dr.TargetY) <= zoneRadius {
				aliveInZone = true
				break
			}
		}
		if !aliveInZone {
			// plus de survivant dans cette zone : on libère le drone
			dr.HasTarget = false
			if dr.Mode == ModeResponding {
				dr.Mode = ModeSearching
				angle := rand.Float64() * 2 * math.Pi
				dr.Vx = math.Cos(angle) * dr.Speed
				dr.Vy = math.Sin(angle) * dr.Speed
			}
		}
	}

	// 0) Timeout renfort paramétrable pour éviter les blocages
	dureeEngagement := cfg.dureeEngagement
	if dureeEngagement <= 0 {
		dureeEngagement = 8.0
	}
	if dr.Mode == ModeResponding {
		dr.RespondTimer += cfg.TimeStep
		if dr.RespondTimer > dureeEngagement {
			dr.Mode = ModeSearching
			dr.HasTarget = false
			angle := rand.Float64() * 2 * math.Pi
			dr.Vx = math.Cos(angle) * dr.Speed
			dr.Vy = math.Sin(angle) * dr.Speed
		}
	} else {
		dr.RespondTimer = 0
	}

	// Mode Hovering : ne bouge plus
	if dr.Mode == ModeHovering {
		dr.Vx, dr.Vy = 0, 0
		return
	}

	// check distance avec le point de charge le plus proche
	nearestX, nearestY := findNearestChargingPoint(dr.X, dr.Y, env.ChargingPoints)
	distToNearest := distance(dr.X, dr.Y, nearestX, nearestY)

	// si autonomie <= 1.1 * temps estimé pour atteindre le point de charge, retour (sécurité)
	timeToReach := distToNearest / dr.Speed
	if dr.Mode != ModeReturning && dr.RemainingAutonomy <= 1.1*timeToReach {
		dr.Mode = ModeReturning
		dr.HasTarget = true
		dr.TargetX = nearestX
		dr.TargetY = nearestY
	}

	dt := cfg.TimeStep

	// 3) Mouvement selon le mode
	switch dr.Mode {
	case ModeSearching:
		tauxExploration := cfg.tauxExploration
		if tauxExploration <= 0 {
			tauxExploration = 0.02
		}

		if rand.Float64() < tauxExploration {

			bestAngle := rand.Float64() * 2 * math.Pi
			bestScore := math.Inf(-1)

			for k := 0; k < 8; k++ {
				angle := float64(k) * math.Pi / 4
				nx := dr.X + math.Cos(angle)*30
				ny := dr.Y + math.Sin(angle)*30

				ix := int(nx / 20)
				iy := int(ny / 20)

				if ix >= 0 && iy >= 0 &&
					ix < len(env.Heatmap) && iy < len(env.Heatmap[0]) {

					h := env.Heatmap[ix][iy]
					score := -h + rand.Float64()*0.1

					if score > bestScore {
						bestScore = score
						bestAngle = angle
					}
				}
			}

			dr.Vx = math.Cos(bestAngle) * dr.Speed
			dr.Vy = math.Sin(bestAngle) * dr.Speed
		}

	case ModeResponding:
		if dr.HasTarget {
			dx := dr.TargetX - dr.X
			dy := dr.TargetY - dr.Y
			dist := math.Hypot(dx, dy)
			// Tant qu'on est loin de la zone, on se rapproche
			if dist > zoneRadius*0.8 {
				dr.Vx = dx / dist * dr.Speed
				dr.Vy = dy / dist * dr.Speed
			} else {
				// Une fois DANS la zone, on passe en recherche locale
				dr.Mode = ModeSearching
				angle := rand.Float64() * 2 * math.Pi
				dr.Vx = math.Cos(angle) * dr.Speed
				dr.Vy = math.Sin(angle) * dr.Speed
			}
		}

	case ModeReturning:
		// retour au pt de charge le plus proche
		dx := dr.TargetX - dr.X
		dy := dr.TargetY - dr.Y
		dist := math.Hypot(dx, dy)
		if dist > 5 {
			dr.Vx = dx / dist * dr.Speed
			dr.Vy = dy / dist * dr.Speed
		} else {
			// arrivé près du point de charge, reset auto et recherche
			dr.RemainingAutonomy = dr.Autonomy
			dr.Mode = ModeSearching
			dr.HasTarget = false
			//  direction random
			angle := rand.Float64() * 2 * math.Pi
			dr.Vx = math.Cos(angle) * dr.Speed
			dr.Vy = math.Sin(angle) * dr.Speed
		}
	}

	// 4) Mise à jour position
	dr.X += dr.Vx * dt
	dr.Y += dr.Vy * dt

	// 5) Bords
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

	// Si le drone est en recherche locale autour d'une trace,
	// on le force à rester dans le cercle de rayon zoneRadius autour de TargetX/TargetY
	if dr.HasTarget && dr.Mode == ModeSearching {
		dx := dr.X - dr.TargetX
		dy := dr.Y - dr.TargetY
		dist := math.Hypot(dx, dy)
		if dist > zoneRadius {
			// on le ramène sur le bord du cercle
			if dist > 0 {
				dr.X = dr.TargetX + dx/dist*zoneRadius
				dr.Y = dr.TargetY + dy/dist*zoneRadius
			} else {
				// au cas où (exactement au centre), petit déplacement aléatoire
				angle := rand.Float64() * 2 * math.Pi
				dr.X = dr.TargetX + math.Cos(angle)*zoneRadius*0.5
				dr.Y = dr.TargetY + math.Sin(angle)*zoneRadius*0.5
			}
		}
	}

	// 6) Consommation d'autonomie (temps écoulé sur ce pas)
	dr.RemainingAutonomy -= dt
	if dr.RemainingAutonomy < 0 {
		dr.RemainingAutonomy = 0
	}

	// 7) Si en mode retour, on ignore traces et survivants
	if dr.Mode == ModeReturning {
		return
	}

	detRadius := dr.DetectionRadius
	if detRadius <= 0 {
		detRadius = cfg.DetectionRadius
		if detRadius <= 0 {
			detRadius = 40
		}
	}

	// 8) Traces
	for ti := range env.Traces {
		tr := &env.Traces[ti]
		if tr.Consumed {
			continue
		}
		if distance(dr.X, dr.Y, tr.X, tr.Y) <= detRadius+tr.Radius {
			// La trace disparaît ici après détection
			// On appelle les renforts UNE SEULE FOIS
			if !tr.Activated {
				tr.Activated = true
				callNeighborsForHelp(env, d.index, ti)
			}
		}
	}

	// 9) Survivants
	// 9) Survivants
	for si := range env.Survivors {
		s := &env.Survivors[si]
		if s.Saved {
			continue
		}
		if distance(dr.X, dr.Y, s.X, s.Y) <= detRadius+s.Radius {
			// Le drone détecte le survivant
			s.Saved = true
			dr.FoundID = s.ID

			// Il n'a plus de cible spécifique
			dr.HasTarget = false

			// S'il était en mode renfort, il repasse en recherche
			if dr.Mode != ModeReturning {
				dr.Mode = ModeSearching
			}

			// IMPORTANT : on NE touche pas à dr.Vx / dr.Vy,
			// il continue son chemin comme si de rien n’était
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
		if dist <= cfg.rayonAide {
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

func defaultChargingPoints(cfg SimConfig) []ChargingPoint {
	w := cfg.Width
	h := cfg.Height

	points := []ChargingPoint{
		{ID: 0, X: w * 0.25, Y: h * 0.25}, // centre du rect haut-gauche
		{ID: 1, X: w * 0.50, Y: h * 0.50}, // centre du rect total
		{ID: 2, X: w * 0.75, Y: h * 0.25}, // centre du rect haut-droite
		{ID: 3, X: w * 0.75, Y: h * 0.75}, // centre du rect bas-droite
		{ID: 4, X: w * 0.25, Y: h * 0.75}, //  centre du rect bas-gauche
	}

	return points
}

func addChargingPoint(cfg *SimConfig, x, y float64) {
	id := len(cfg.ChargingPoints)
	cfg.ChargingPoints = append(cfg.ChargingPoints, ChargingPoint{
		ID: id,
		X:  x,
		Y:  y,
	})
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
		rayonAide:        150,
		MaxHelpersPerHit: 3,
		TimeStep:         0.1,
		DroneTypes:       nil,
		ChargingPoints:   nil,
		BaseX:            -1,
		BaseY:            -1,

		tailleIndice:    1.5,
		tauxExploration: 0.02,
		dureeEngagement: 8.0,
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
	if cfg.NumDrones <= 0 && len(cfg.DroneTypes) == 0 {
		cfg.NumDrones = 10
	}
	if cfg.DroneSpeed <= 0 {
		cfg.DroneSpeed = 50
	}
	if cfg.DetectionRadius <= 0 {
		cfg.DetectionRadius = 40
	}
	if cfg.rayonAide <= 0 {
		cfg.rayonAide = 150
	}
	if cfg.MaxHelpersPerHit <= 0 {
		cfg.MaxHelpersPerHit = 3
	}
	if cfg.TimeStep <= 0 {
		cfg.TimeStep = 0.1
	}
	if cfg.tailleIndice <= 0 {
		cfg.tailleIndice = 1.5
	}
	if cfg.tauxExploration <= 0 {
		cfg.tauxExploration = 0.02
	}
	if cfg.dureeEngagement <= 0 {
		cfg.dureeEngagement = 8.0
	}
	if len(cfg.ChargingPoints) == 0 {
		cfg.ChargingPoints = defaultChargingPoints(cfg)
	}

	rand.Seed(time.Now().UnixNano())

	// pos de base : si pas définie, on centre
	if cfg.BaseX <= 0 && cfg.BaseY <= 0 {
		cfg.BaseX = cfg.Width / 2
		cfg.BaseY = cfg.Height / 2
	}
	centerX := cfg.BaseX
	centerY := cfg.BaseY

	var drones []Drone
	var agents []Agent

	// si config, construction drones typés
	if len(cfg.DroneTypes) > 0 {
		for _, dt := range cfg.DroneTypes {
			if dt.Count <= 0 {
				continue
			}
			speed := dt.Speed
			if speed <= 0 {
				speed = cfg.DroneSpeed
			}
			autonomy := dt.Autonomy
			if autonomy <= 0 {
				autonomy = 10.0 // default time-based autonomy in seconds
			}

			detR := dt.DetectionRadius
			if detR <= 0 {
				detR = cfg.DetectionRadius
				if detR <= 0 {
					detR = 40 // final fallback
				}
			}

			for i := 0; i < dt.Count; i++ {
				angle := rand.Float64() * 2 * math.Pi
				drone := Drone{
					ID:                len(drones),
					X:                 centerX,
					Y:                 centerY,
					Vx:                math.Cos(angle) * speed,
					Vy:                math.Sin(angle) * speed,
					Mode:              ModeSearching,
					TargetX:           0,
					TargetY:           0,
					HasTarget:         false,
					FoundID:           -1,
					RespondTimer:      0,
					Speed:             speed,
					Weight:            dt.Weight,
					Autonomy:          autonomy,
					RemainingAutonomy: autonomy,
					DetectionRadius:   detR,
				}
				drones = append(drones, drone)
				agents = append(agents, NewDroneAgent(drone.ID, &cfg))
			}
		}
	} else {
		// config de base : drones homogènes
		drones = make([]Drone, cfg.NumDrones)
		agents = make([]Agent, cfg.NumDrones)

		detR := cfg.DetectionRadius
		if detR <= 0 {
			detR = 40
		}

		for i := range drones {
			angle := rand.Float64() * 2 * math.Pi
			speed := cfg.DroneSpeed
			autonomy := 20.0 // default time-based autonomy in seconds

			drones[i] = Drone{
				ID:                i,
				X:                 centerX,
				Y:                 centerY,
				Vx:                math.Cos(angle) * speed,
				Vy:                math.Sin(angle) * speed,
				Mode:              ModeSearching,
				TargetX:           0,
				TargetY:           0,
				HasTarget:         false,
				FoundID:           -1,
				RespondTimer:      0,
				Speed:             speed,
				Weight:            1.0,
				Autonomy:          autonomy,
				RemainingAutonomy: autonomy,
				DetectionRadius:   detR,
			}
			agents[i] = NewDroneAgent(i, &cfg)
		}
	}

	// Survivants
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

	traceBaseRadius := cfg.DetectionRadius
	if traceBaseRadius <= 0 {
		if len(drones) > 0 && drones[0].DetectionRadius > 0 {
			traceBaseRadius = drones[0].DetectionRadius
		} else {
			traceBaseRadius = 40
		}
	}

	// Traces : une par survivant
	var traces []Trace
	if len(survivors) > 0 {
		traces = make([]Trace, len(survivors))
		for i := range traces {
			sv := &survivors[i]

			// Rayon de la trace : param entraînable
			traceRadius := traceBaseRadius * cfg.tailleIndice

			// On veut que le survivant soit À L’INTÉRIEUR du cercle
			margin := sv.Radius + 3
			rMax := traceRadius - margin
			if rMax < 0 {
				rMax = 0
			}

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
				ID:         i,
				X:          x,
				Y:          y,
				Radius:     traceRadius,
				Consumed:   false,
				SurvivorID: sv.ID,
				Activated:  false,
			}
		}
	} else {
		// fallback si vraiment aucun survivant : traces libres sans lien
		traces = make([]Trace, cfg.NumTraces)
		for i := range traces {
			traces[i] = Trace{
				ID:         i,
				X:          rand.Float64() * cfg.Width,
				Y:          rand.Float64() * cfg.Height,
				Radius:     traceBaseRadius * cfg.tailleIndice,
				Consumed:   false,
				SurvivorID: -1,
				Activated:  false,
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	gridW := int(cfg.Width / 20)
	gridH := int(cfg.Height / 20)

	heat := make([][]float64, gridW)
	for i := range heat {
		heat[i] = make([]float64, gridH)
	}

	s.env = Environment{
		Config:         cfg,
		Drones:         drones,
		Survivors:      survivors,
		Traces:         traces,
		ChargingPoints: cfg.ChargingPoints,
		Time:           0,
		Finished:       false,
		Stats:          SimStats{},
		Heatmap:        heat,
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
	for _, d := range s.env.Drones {
		ix := int(d.X / 20)
		iy := int(d.Y / 20)

		if ix >= 0 && iy >= 0 && ix < len(s.env.Heatmap) && iy < len(s.env.Heatmap[0]) {
			s.env.Heatmap[ix][iy] += 1
		}
	}

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

	// 🔥 Mise à jour des traces : elles disparaissent quand le survivant associé est sauvé
	for i := range s.env.Traces {
		tr := &s.env.Traces[i]
		if tr.Consumed {
			continue
		}
		if tr.SurvivorID >= 0 && tr.SurvivorID < len(s.env.Survivors) {
			sv := &s.env.Survivors[tr.SurvivorID]
			if sv.Saved {
				tr.Consumed = true
			}
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

var (
	sim        *Simulation
	baseConfig SimConfig
)

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
	var reqCfg SimConfig
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&reqCfg); err != nil {
			reqCfg = defaultConfig()
		}
	} else {
		reqCfg = defaultConfig()
	}

	// On repart de la config de base
	cfg := baseConfig

	if len(reqCfg.DroneTypes) > 0 {
		// Si le front envoie des types, on les utilise
		cfg.DroneTypes = reqCfg.DroneTypes
		// On remet NumDrones à 0 pour forcer la logique par types dans Simulation.Reset
		cfg.NumDrones = 0
	} else if len(cfg.DroneTypes) == 0 && reqCfg.NumDrones > 0 {
		// ancienne méthode (si pas de types)
		cfg.NumDrones = reqCfg.NumDrones
	}

	// On laisse l'utilisateur changer certains paramètres
	if reqCfg.NumSurvivors > 0 {
		cfg.NumSurvivors = reqCfg.NumSurvivors
	}
	if reqCfg.NumTraces > 0 {
		cfg.NumTraces = reqCfg.NumTraces
	}
	if reqCfg.DroneSpeed > 0 {
		cfg.DroneSpeed = reqCfg.DroneSpeed
	}
	if reqCfg.DetectionRadius > 0 {
		cfg.DetectionRadius = reqCfg.DetectionRadius
	}
	// On ne touche NumDrones que si on n'utilise pas de types de drones
	if len(cfg.DroneTypes) == 0 && reqCfg.NumDrones > 0 {
		cfg.NumDrones = reqCfg.NumDrones
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

func loadConfig(path string) SimConfig {
	file, err := os.Open(path)
	if err != nil {
		log.Println("No config file found, using defaults.")
		return defaultConfig()
	}
	defer file.Close()

	var cfg SimConfig
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		log.Println("Config file invalid, using defaults:", err)
		return defaultConfig()
	}
	return cfg
}

// Config de policy apprise par le batch
type LearnedPolicyConfig struct {
	rayonAide        float64 `json:"rayonAide"`
	MaxHelpersPerHit int     `json:"maxHelpersPerHit"`
	tailleIndice     float64 `json:"tailleIndice"`
	tauxExploration  float64 `json:"tauxExploration"`
	dureeEngagement  float64 `json:"dureeEngagement"`
}

func loadBestPolicy(path string) (LearnedPolicyConfig, bool) {
	file, err := os.Open(path)
	if err != nil {
		return LearnedPolicyConfig{}, false
	}
	defer file.Close()

	var pol LearnedPolicyConfig
	if err := json.NewDecoder(file).Decode(&pol); err != nil {
		log.Println("best_policy.json invalide, ignoré:", err)
		return LearnedPolicyConfig{}, false
	}
	return pol, true
}

func main() {
	// Mode batch offline
	print("os.Getenv(TRAIN_BATCH) =", os.Getenv("TRAIN_BATCH"))
	if os.Getenv("TRAIN_BATCH") == "1" {
		RunBatchTraining()
		return
	}

	// Mode serveur normal
	cfg := loadConfig("config.json")

	// surcharge avec la policy apprise si présente
	if _, ok := loadBestPolicy("best_policy.json"); ok {
		cfg.rayonAide = 356.8
		cfg.MaxHelpersPerHit = 2
		cfg.tailleIndice = 2.97
		cfg.tauxExploration = 0.012
		cfg.dureeEngagement = 8.1
		log.Printf("Using learned policy: rayonAide=%.1f, maxHelpers=%d, traceFactor=%.2f, exploreRate=%.3f, timeout=%.1fs",
			cfg.rayonAide, cfg.MaxHelpersPerHit, cfg.tailleIndice, cfg.tauxExploration, cfg.dureeEngagement)
	}

	// on garde cette config comme "base" avec entraînement appliqué
	baseConfig = cfg

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

func findNearestChargingPoint(droneX, droneY float64, points []ChargingPoint) (float64, float64) {
	if len(points) == 0 {
		return 0, 0 // fallback, but shouldn't happen
	}
	minDist := math.Inf(1)
	var nearestX, nearestY float64
	for _, p := range points {
		dist := distance(droneX, droneY, p.X, p.Y)
		if dist < minDist {
			minDist = dist
			nearestX = p.X
			nearestY = p.Y
		}
	}
	return nearestX, nearestY
}

func distance(x1, y1, x2, y2 float64) float64 {
	return math.Hypot(x1-x2, y1-y2)
}
