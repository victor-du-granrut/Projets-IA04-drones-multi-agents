const canvas = document.getElementById("sim-canvas");
const ctx = canvas.getContext("2d");

const toggleBtn = document.getElementById("toggle-btn");
const statusText = document.getElementById("status-text");
const configForm = document.getElementById("config-form");
const heatmapToggle = document.getElementById("toggle-heatmap");
heatmapToggle.addEventListener("change", (e) => {
  showHeatmap = e.target.checked;
});


let currentWorld = null;
let running = true;
let showHeatmap = true;


async function apiReset(config) {
  const res = await fetch("/api/reset", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  if (!res.ok) {
    console.error("Erreur reset", res.status);
    return;
  }
  currentWorld = await res.json();
}

async function apiState() {
  const res = await fetch("/api/state");
  if (!res.ok) {
    console.error("Erreur state", res.status);
    return;
  }
  currentWorld = await res.json();
}

async function apiToggle() {
  const res = await fetch("/api/toggle", { method: "POST" });
  if (!res.ok) {
    console.error("Erreur toggle", res.status);
    return;
  }
  const data = await res.json();
  running = data.running;
  toggleBtn.textContent = running ? "⏸ Pause" : "▶ Reprendre";
}

function drawWorld() {
  if (!currentWorld) return;

  const { config, drones, survivors, traces, chargingPoints, time, finished, stats } =
    currentWorld;

  const w = canvas.width;
  const h = canvas.height;

  const scaleX = w / config.width;
  const scaleY = h / config.height;

  // Fond sombre
  ctx.clearRect(0, 0, w, h);
  ctx.globalAlpha = 1;
  ctx.fillStyle = "#020617"; // très sombre
  ctx.fillRect(0, 0, w, h);

  // --- Traces de vie : n'afficher QUE la partie en intersection avec un ou plusieurs champs de vision ---
  traces.forEach((tr) => {
    if (tr.consumed) return; // la trace est "morte" côté back

    // On récupère les drones dont le champ de vision intersecte la trace
    const seeingDrones = [];
    for (const d of drones) {
      const detRDrone = d.detectionRadius || config.detectionRadius || 40;
      const dist = Math.hypot(d.x - tr.x, d.y - tr.y);
      if (dist <= detRDrone + tr.radius) {
        seeingDrones.push(d);
      }
    }

    if (seeingDrones.length === 0) return; // aucun drone ne voit cette trace -> rien à dessiner

    const cx = tr.x * scaleX;
    const cy = tr.y * scaleY;
    const traceR = tr.radius * scaleX; // on suppose scaleX ~ scaleY

    ctx.save();

    // 1) On clip sur le cercle de la trace
    ctx.beginPath();
    ctx.arc(cx, cy, traceR, 0, Math.PI * 2);
    ctx.clip();

    // 2) On remplit avec la couleur de trace, mais uniquement à l'intérieur
    //    des champs de vision des drones qui la voient
    ctx.globalAlpha = 0.35;
    ctx.fillStyle = "rgba(168,85,247,1)";

    seeingDrones.forEach((d) => {
      const vx = d.x * scaleX;
      const vy = d.y * scaleY;
      const detR =
        (d.detectionRadius || config.detectionRadius || 40) * scaleX;

      ctx.beginPath();
      ctx.arc(vx, vy, detR, 0, Math.PI * 2);
      ctx.fill();
    });

    ctx.globalAlpha = 1;
    ctx.restore();
  });
  // --- HEATMAP EXPLORATION (thermal realistic) ---
  if (currentWorld.heatmap && showHeatmap) {
    const heat = currentWorld.heatmap;
    const cellW = canvas.width / heat.length;
    const cellH = canvas.height / heat[0].length;

    let maxVal = 1;
    for (let i = 0; i < heat.length; i++) {
      for (let j = 0; j < heat[0].length; j++) {
        if (heat[i][j] > maxVal) maxVal = heat[i][j];
      }
    }

    function heatColor(t) {
      // t in [0,1]  blue → cyan → green → yellow → red
      const r = Math.min(255, Math.max(0,
        255 * Math.max(0, Math.min(1, (t - 0.5) * 2))
      ));
      const g = Math.min(255, Math.max(0,
        255 * Math.min(1, 1 - Math.abs(t - 0.5) * 2)
      ));
      const b = Math.min(255, Math.max(0,
        255 * Math.max(0, Math.min(1, (0.5 - t) * 2))
      ));
      return `rgba(${r|0},${g|0},${b|0},0.35)`;
    }

    for (let i = 0; i < heat.length; i++) {
      for (let j = 0; j < heat[0].length; j++) {
        const v = heat[i][j];
        if (v <= 0) continue;

        const t = Math.min(v / maxVal, 1);
        ctx.fillStyle = heatColor(t);
        ctx.fillRect(i * cellW, j * cellH, cellW, cellH);
      }
    }
  }


// --- budget ---
const MAX_BUDGET = 100000;
const droneInputs = document.querySelectorAll(".drone-input");
const budgetDisplay = document.getElementById("budget-display");
const totalCostDisplay = document.getElementById("total-cost");
const errorMsg = document.getElementById("budget-error");
const submitBtn = configForm.querySelector("button[type='submit']");

function updateBudget() {
  let total = 0;
  droneInputs.forEach(input => {
    const price = parseInt(input.dataset.price);
    const count = parseInt(input.value) || 0;
    total += price * count;
  });

  totalCostDisplay.textContent = total;
  const remaining = MAX_BUDGET - total;
  budgetDisplay.textContent = remaining;

  if (remaining < 0) {
    errorMsg.style.display = "block";
    submitBtn.disabled = true;
    budgetDisplay.style.color = "#ef4444";
  } else {
    errorMsg.style.display = "none";
    submitBtn.disabled = false;
    budgetDisplay.style.color = "#22c55e";
  }
}

droneInputs.forEach(input => {
  input.addEventListener("input", updateBudget);
});

updateBudget();


  // --- Survivants : cachés tant qu'ils ne sont pas trouvés ---
  survivors.forEach((s) => {
    if (!s.saved) return; // on ignore les non trouvés

    ctx.beginPath();
    const r = (s.radius || 6) * scaleX;
    ctx.arc(s.x * scaleX, s.y * scaleY, r, 0, Math.PI * 2);
    ctx.fillStyle = "rgba(34,197,94,0.95)"; // vert vif pour "trouvé"
    ctx.fill();
  });

  // --- Points de charge ---
  (currentWorld.chargingPoints || []).forEach((cp) => {
    ctx.beginPath();
    const r = 12 * scaleX;
    ctx.arc(cp.x * scaleX, cp.y * scaleY, r, 0, Math.PI * 2);
    ctx.fillStyle = "rgba(255,215,0,0.95)"; // gold
    ctx.fill();
  });

  // --- Drones + cercle de vision ---
  drones.forEach((d) => {
    const x = d.x * scaleX;
    const y = d.y * scaleY;

    // Rayon de vision (détection)
    const detR =
      (d.detectionRadius || config.detectionRadius || 40) * scaleX;

    // Cercle de vision
    ctx.beginPath();
    ctx.arc(x, y, detR, 0, Math.PI * 2);
    ctx.strokeStyle = "rgba(148,163,184,0.25)"; // gris clair transparent
    ctx.lineWidth = 1;
    ctx.stroke();

    // Corps du drone (on garde la flèche)
    let color = "rgba(56,189,248,0.95)"; // bleu
    if (d.state === "responding") {
      color = "rgba(251,191,36,0.95)"; // jaune
    } else if (d.state === "hovering") {
      color = "rgba(34,197,94,0.95)"; // vert
    } else if (d.state === "returning") {
      color = "rgba(255,0,0,0.95)"; // rouge 
    }

    const angle = Math.atan2(d.vy, d.vx);
    const size = 8;

    ctx.save();
    ctx.translate(x, y);
    if (d.vx !== 0 || d.vy !== 0) ctx.rotate(angle);

    ctx.beginPath();
    ctx.moveTo(size, 0);
    ctx.lineTo(-size * 0.8, size * 0.6);
    ctx.lineTo(-size * 0.8, -size * 0.6);
    ctx.closePath();
    ctx.fillStyle = color;
    ctx.fill();
    ctx.restore();
  });

  const remaining = survivors.filter((s) => !s.saved).length;
  statusText.textContent = finished
    ? `Simulation terminée – Temps : ${stats.totalTime.toFixed(
        1
      )} s – Survivants sauvés : ${stats.savedSurvivors}/${stats.totalSurvivors}`
    : `Temps : ${time.toFixed(1)} s | Drones : ${
        drones.length
      } | Survivants restants : ${remaining}`;

  // Si terminé : overlay avec les stats
  if (finished) {
    // désactiver pause/reprise
    toggleBtn.disabled = true;

    ctx.save();
    ctx.globalAlpha = 0.85;
    ctx.fillStyle = "#020617";
    const boxW = w * 0.6;
    const boxH = h * 0.35;
    const boxX = (w - boxW) / 2;
    const boxY = (h - boxH) / 2;
    ctx.fillRect(boxX, boxY, boxW, boxH);

    ctx.globalAlpha = 1;
    ctx.strokeStyle = "rgba(148,163,184,0.7)";
    ctx.lineWidth = 2;
    ctx.strokeRect(boxX, boxY, boxW, boxH);

    ctx.fillStyle = "#e5e7eb";
    ctx.font = "18px system-ui";
    ctx.fillText("Simulation terminée", boxX + 20, boxY + 30);

    ctx.font = "14px system-ui";
    const lines = [
      `Temps total : ${stats.totalTime.toFixed(2)} s`,
      `Survivants sauvés : ${stats.savedSurvivors} / ${stats.totalSurvivors}`,
      `Nombre de drones : ${stats.drones}`,
      `Traces de vie utilisées : ${stats.tracesConsumed} / ${stats.traces}`,
      "",
      'Clique sur "Appliquer & reset" pour relancer une nouvelle simulation.',
    ];

    let y = boxY + 60;
    lines.forEach((line) => {
      ctx.fillText(line, boxX + 20, y);
      y += 22;
    });

    ctx.restore();
  } else {
    toggleBtn.disabled = false;
  }
}

async function loop() {
  try {
    await apiState();
  } catch (e) {
    console.error(e);
  }

  drawWorld();
  setTimeout(loop, 50);
}

toggleBtn.addEventListener("click", () => {
  apiToggle().catch(console.error);
});

configForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const data = new FormData(configForm);

  // Construction des types de drones basés sur les inputs
  const droneTypes = [
    {
      name: "Scout",
      count: Number(data.get("count_scout")),
      speed: 90,
      weight: 1.0,
      autonomy: 15.0,
      detectionRadius: 30
    },
    {
      name: "Standard",
      count: Number(data.get("count_standard")),
      speed: 60,
      weight: 2.0,
      autonomy: 25.0,
      detectionRadius: 50
    },
    {
      name: "Heavy",
      count: Number(data.get("count_heavy")),
      speed: 30,
      weight: 5.0,
      autonomy: 40.0,
      detectionRadius: 80
    }
  ];

  const filteredTypes = droneTypes.filter(t => (t.count || 0) > 0);

  const config = {
    droneTypes: filteredTypes,
    numSurvivors: Number(data.get("numSurvivors")),
  };


  await apiReset(config);
  toggleBtn.disabled = false;
  toggleBtn.textContent = "⏸ Pause";
  drawWorld();
});

(async function init() {
  await apiReset({});
  await apiState();
  drawWorld();
  loop();
})();
