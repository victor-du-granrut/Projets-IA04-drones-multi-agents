package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"time"
)

// Paramètres entraînables offline
type TrainParams struct {
	rayonAide        float64
	MaxHelpersPerHit int
	tailleIndice     float64
	tauxExploration  float64
	dureeEngagement  float64
}

// Lance le batch d'entraînement offline et écrit best_policy.json
func RunBatchTraining() {
	baseCfg := loadConfig("config.json")

	rand.Seed(time.Now().UnixNano())

	const numCandidates = 80     // nombre de combinaisons testées
	const runsPerCandidate = 5   // nb de répétitions par combinaison
	const maxStepsPerSim = 20000 // sécurité

	bestScore := math.Inf(-1)
	var bestParams TrainParams
	var bestStats SimStats

	fmt.Println("=== Batch training drones (offline) ===")
	fmt.Printf("Base scenario: survivors=%d, traces=%d, droneTypes=%d\n",
		baseCfg.NumSurvivors, baseCfg.NumTraces, len(baseCfg.DroneTypes))

	for i := 0; i < numCandidates; i++ {
		p := randomTrainParams()

		var totalScore float64
		var aggStats SimStats

		for r := 0; r < runsPerCandidate; r++ {
			cfg := applyTrainParams(baseCfg, p)
			stats := runSimulationOnce(cfg, maxStepsPerSim)

			s := scoreStats(stats)
			totalScore += s

			aggStats.TotalTime += stats.TotalTime
			aggStats.SavedSurvivors += stats.SavedSurvivors
			aggStats.TotalSurvivors = stats.TotalSurvivors
		}

		avgScore := totalScore / float64(runsPerCandidate)
		avgStats := SimStats{
			TotalTime:      aggStats.TotalTime / float64(runsPerCandidate),
			SavedSurvivors: int(math.Round(float64(aggStats.SavedSurvivors) / float64(runsPerCandidate))),
			TotalSurvivors: aggStats.TotalSurvivors,
		}

		fmt.Printf("[candidat %2d] rayonAide=%.1f, MaxHelpers=%d, TraceFactor=%.2f, Explore=%.3f, Timeout=%.1fs -> score=%.2f, avgTime=%.1fs, saved=%d/%d\n",
			i, p.rayonAide, p.MaxHelpersPerHit, p.tailleIndice, p.tauxExploration, p.dureeEngagement,
			avgScore, avgStats.TotalTime, avgStats.SavedSurvivors, avgStats.TotalSurvivors)

		if avgScore > bestScore {
			bestScore = avgScore
			bestParams = p
			bestStats = avgStats
		}
	}

	fmt.Println("\n=== MEILLEURE CONFIG TROUVÉE ===")
	fmt.Printf("rayonAide=%.1f, MaxHelpers=%d, TraceFactor=%.2f, Explore=%.3f, Timeout=%.1fs\n",
		bestParams.rayonAide, bestParams.MaxHelpersPerHit, bestParams.tailleIndice, bestParams.tauxExploration, bestParams.dureeEngagement)
	fmt.Printf("Performance moyenne: sauvés=%d/%d, temps moyen=%.1fs, score=%.2f\n",
		bestStats.SavedSurvivors, bestStats.TotalSurvivors, bestStats.TotalTime, bestScore)

	// Sauvegarde dans best_policy.json
	policy := LearnedPolicyConfig{
		rayonAide:        bestParams.rayonAide,
		MaxHelpersPerHit: bestParams.MaxHelpersPerHit,
		tailleIndice:     bestParams.tailleIndice,
		tauxExploration:  bestParams.tauxExploration,
		dureeEngagement:  bestParams.dureeEngagement,
	}
	if err := saveBestPolicy("best_policy.json", policy); err != nil {
		log.Println("Erreur lors de l'écriture de best_policy.json:", err)
	} else {
		fmt.Println("\nFichier best_policy.json écrit avec succès.")
		fmt.Println("Au prochain lancement du serveur web, cette politique sera utilisée par défaut.")
	}
}

// Plages de recherche
func randomTrainParams() TrainParams {
	return TrainParams{
		rayonAide:        100 + rand.Float64()*300,   // 100 à 400
		MaxHelpersPerHit: 1 + rand.Intn(6),           // 1 à 6
		tailleIndice:     1.0 + rand.Float64()*2.0,   // 1.0 à 3.0
		tauxExploration:  0.01 + rand.Float64()*0.19, // 0.01 à 0.20
		dureeEngagement:  3.0 + rand.Float64()*9.0,   // 3 à 12 secondes
	}
}

// Applique les params à la config de base
func applyTrainParams(base SimConfig, p TrainParams) SimConfig {
	cfg := base
	cfg.rayonAide = p.rayonAide
	cfg.MaxHelpersPerHit = p.MaxHelpersPerHit
	cfg.tailleIndice = p.tailleIndice
	cfg.tauxExploration = p.tauxExploration
	cfg.dureeEngagement = p.dureeEngagement
	return cfg
}

// Lance UNE simulation hors-ligne jusqu'à la fin ou maxSteps
func runSimulationOnce(cfg SimConfig, maxSteps int) SimStats {
	s := NewSimulation(cfg)

	for i := 0; i < maxSteps; i++ {
		s.step() // fonction interne, même package
		env := s.Snapshot()
		if env.Finished {
			return env.Stats
		}
	}

	// si la simu n'a pas fini, on renvoie quand même des stats
	env := s.Snapshot()
	stats := env.Stats
	stats.TotalTime = env.Time
	stats.TotalSurvivors = len(env.Survivors)
	stats.SavedSurvivors = 0
	for _, sv := range env.Survivors {
		if sv.Saved {
			stats.SavedSurvivors++
		}
	}
	return stats
}

// Fonction de score : plus de survivants et moins de temps = meilleur
func scoreStats(stats SimStats) float64 {
	if stats.TotalSurvivors == 0 {
		return -1e9
	}
	if stats.SavedSurvivors == 0 {
		return -1e9
	}
	survivalRate := float64(stats.SavedSurvivors) / float64(stats.TotalSurvivors)
	return survivalRate*1000.0 - stats.TotalTime
}

// Sauvegarde le best policy en JSON
func saveBestPolicy(path string, policy LearnedPolicyConfig) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(policy)
}
