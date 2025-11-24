const canvas = document.getElementById("sim-canvas");
const ctx = canvas.getContext("2d");

const toggleBtn = document.getElementById("toggle-btn");
const statusText = document.getElementById("status-text");
const configForm = document.getElementById("config-form");

let currentWorld = null;
let running = true;

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

  const { config, drones, survivors, traces, time, finished, stats } =
    currentWorld;

  const w = canvas.width;
  const h = canvas.height;

  const scaleX = w / config.width;
  const scaleY = h / config.height;

  ctx.clearRect(0, 0, w, h);

  ctx.globalAlpha = 1;
  ctx.fillStyle = "rgba(15,23,42,0.6)";
  ctx.fillRect(0, 0, w, h);

  // Traces
  traces.forEach((tr) => {
    if (tr.consumed) return;
    ctx.beginPath();
    ctx.strokeStyle = "rgba(168,85,247,0.7)";
    ctx.lineWidth = 2;
    ctx.arc(tr.x * scaleX, tr.y * scaleY, tr.radius * scaleX, 0, Math.PI * 2);
    ctx.stroke();
  });

  // Survivants
  survivors.forEach((s) => {
    ctx.beginPath();
    ctx.fillStyle = s.saved
      ? "rgba(34,197,94,0.9)"
      : "rgba(249,115,22,0.9)";
    const r = s.radius * scaleX;
    ctx.arc(s.x * scaleX, s.y * scaleY, r, 0, Math.PI * 2);
    ctx.fill();
  });

  // Drones
  drones.forEach((d) => {
    let color = "rgba(56,189,248,0.95)";
    if (d.state === "responding") {
      color = "rgba(251,191,36,0.95)";
    } else if (d.state === "hovering") {
      color = "rgba(34,197,94,0.95)";
    }

    const x = d.x * scaleX;
    const y = d.y * scaleY;
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
    ctx.globalAlpha = 0.75;
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
      "Clique sur \"Appliquer & reset\" pour relancer une nouvelle simulation.",
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

  const config = {
    numDrones: Number(data.get("numDrones")),
    numSurvivors: Number(data.get("numSurvivors")),
    numTraces: Number(data.get("numTraces")),
    droneSpeed: Number(data.get("droneSpeed")),
    detectionRadius: Number(data.get("detectionRadius")),
    commRadius: Number(data.get("commRadius")),
    maxHelpersPerHit: Number(data.get("maxHelpersPerHit")),
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
