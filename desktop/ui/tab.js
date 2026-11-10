"use strict";
const $ = id => document.getElementById(id);
const bridge = window.zpTab || { toggle(){}, onPeek(){}, onUpdate(){}, onState(){}, onSide(){}, dragStart(){}, drag(){}, dragEnd(){} };

const pad = n => String(n).padStart(2, "0");
const clock = s => { s = Math.max(0, Math.floor(s)); const h = Math.floor(s/3600), m = Math.floor((s%3600)/60); return h ? `${h}:${pad(m)}` : `${m}m`; };

let status = { timer: { active:false }, pause: { active:false } };
let toastTimer = null;

function render() {
  const dot = $("dot");
  dot.className = status.timer.active ? "running" : status.pause.active ? "pause" : "idle";
  $("clock").textContent = status.timer.active ? clock((Date.now() - Date.parse(status.timer.start_time))/1000)
                          : status.pause.active ? clock((Date.now() - Date.parse(status.pause.start_time))/1000)
                          : "";
}
setInterval(render, 1000);

bridge.onUpdate(s => { status = s || status; render(); });
bridge.onState(expanded => $("chev").classList.toggle("expanded", !!expanded));
bridge.onPeek(reason => {
  const tab = $("tab");
  tab.classList.remove("peeking"); void tab.offsetWidth; tab.classList.add("peeking");
  if (reason && reason.text) {
    $("toastText").textContent = reason.text;
    $("toast").classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => $("toast").classList.remove("show"), 8000);
  }
});

// Klicken = ein-/ausklappen, Ziehen = verschieben (rastet am Rand des Bildschirms ein)
let down = null, moved = false;
$("tab").addEventListener("pointerdown", e => {
  if (e.button !== 0) return;
  down = { x: e.screenX, y: e.screenY }; moved = false;
  $("tab").setPointerCapture(e.pointerId);
});
$("tab").addEventListener("pointermove", e => {
  if (!down) return;
  const dx = e.screenX - down.x, dy = e.screenY - down.y;
  if (!moved && Math.hypot(dx, dy) < 5) return;
  if (!moved) { moved = true; bridge.dragStart && bridge.dragStart(); $("tab").classList.add("dragging"); $("toast").classList.remove("show"); }
  bridge.drag && bridge.drag(dx, dy);
});
$("tab").addEventListener("pointerup", e => {
  if (!down) return;
  try { $("tab").releasePointerCapture(e.pointerId); } catch (_) {}
  down = null;
  $("tab").classList.remove("dragging");
  if (moved) { bridge.dragEnd && bridge.dragEnd(); return; }
  $("toast").classList.remove("show"); bridge.toggle();
});
bridge.onSide && bridge.onSide(side => document.body.classList.toggle("left", side === "left"));
render();
