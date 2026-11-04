package main

// Pagina pubblica di sola lettura (nessuna password). Letta dal browser da /pubblico.
const pubHTML = `<!doctype html>
<html lang="it">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex">
<title>Torneo TC Salemi</title>
<link rel="icon" type="image/png" sizes="128x128" href="/favicon.png?v=2">
<link rel="shortcut icon" href="/favicon.ico?v=2">
<link rel="apple-touch-icon" href="/apple-touch-icon.png?v=2">
<style>
:root{--bg:#f3f5f4;--card:#fff;--ink:#1c2420;--mut:#6b756f;--line:#d5dbd8;--acc:#1f6b45;--acc2:#e8f3ec;--win:#d6efd9}
*{box-sizing:border-box}
body{margin:0;font-family:system-ui,"Segoe UI",Arial,sans-serif;background:var(--bg);color:var(--ink);font-size:15px;-webkit-text-size-adjust:100%}
header{background:#1c2420;color:#fff;padding:10px 14px;display:flex;align-items:center;gap:12px}
header img{height:38px;background:#fff;border-radius:8px;padding:3px}
header h1{font-size:17px;margin:0;line-height:1.2}
header small{display:block;color:#b9c5be;font-size:12px;font-weight:400}
.tabs{display:flex;gap:6px;padding:10px 12px 0;overflow-x:auto}
.tabs button,.chips button{white-space:nowrap;border:1px solid var(--line);background:#fff;color:var(--ink);border-radius:99px;padding:8px 14px;font-size:14px;cursor:pointer}
.tabs button.on,.chips button.on{background:var(--acc);border-color:var(--acc);color:#fff}
.chips{display:flex;gap:6px;padding:10px 12px 0;overflow-x:auto}
main{padding:12px 12px 40px}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px 14px;margin-bottom:12px}
h2{margin:0 0 8px;font-size:16px}
.muted{color:var(--mut)}
.bracket{display:flex;gap:18px;overflow-x:auto;padding:4px 2px 12px;scroll-snap-type:x proximity;-webkit-overflow-scrolling:touch}
.round{display:flex;flex-direction:column;min-width:min(260px,80vw);scroll-snap-align:start}
.round h4{margin:0 0 6px;font-size:12px;text-transform:uppercase;letter-spacing:.5px;color:var(--mut)}
.matches{flex:1;display:flex;flex-direction:column;justify-content:space-around}
.match{border:1px solid var(--line);border-radius:8px;background:#fff;margin:5px 0;overflow:hidden}
.pl{display:flex;justify-content:space-between;gap:8px;padding:7px 10px;border-bottom:1px solid #f0f2f1}
.pl:last-of-type{border-bottom:0}
.pl.win{background:var(--win);font-weight:600}
.pl.lose{color:#98a29c}
.pl.tbd,.pl.bye{color:#9aa49e;font-style:italic}
.rk{color:var(--mut);font-size:12px;font-weight:400;white-space:nowrap}
.sd{display:inline-block;min-width:18px;font-size:11px;background:#1c2420;color:#fff;border-radius:4px;text-align:center;margin-right:5px}
.sc{border-top:1px dashed var(--line);padding:4px 10px;background:#fafbfa;font-size:13px;color:#444;min-height:22px}
.champ{margin-top:8px;padding:12px 16px;border-radius:10px;background:linear-gradient(90deg,#f6e7b0,#fff3cf);border:1px solid #e6cf7d;font-weight:600}
.st{display:grid;grid-template-columns:24px 1fr 30px 30px 52px 38px;gap:4px 6px;align-items:center;font-size:14px}
.st .h{font-size:11px;text-transform:uppercase;color:var(--mut)}
.st .n{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.rrm{display:grid;grid-template-columns:34px 1fr auto;gap:6px;padding:8px 0;border-top:1px solid #eceeed;font-size:14px}
.rrm .t{color:var(--mut);font-size:12px}
.crow{display:grid;grid-template-columns:28px 1fr auto;gap:2px 10px;padding:9px 0;border-top:1px solid #eceeed;align-items:center}
.crow .pos{color:var(--mut);font-weight:600}
.crow .cp{text-align:right}
.crow .cp b{font-size:17px}
.crow .cp small{display:block;color:var(--mut);font-size:11px}
.crow .cs{grid-column:2/4;color:var(--mut);font-size:12px}
.foot{text-align:center;color:var(--mut);font-size:12px;margin-top:8px}
@media (min-width:761px){.hm{display:block}}
@media (max-width:760px){.hm{display:none}.st{grid-template-columns:22px 1fr 26px 26px 44px 32px}}
</style>
</head>
<body>
<header>
  <img src="/logo.png" alt="" onerror="this.style.display='none'">
  <h1 id="ttl">Torneo<small id="sub"></small></h1>
</header>
<div class="tabs" id="tabs"></div>
<div class="chips" id="chips"></div>
<main id="main"><div class="card muted">Caricamento...</div></main>
<script>
"use strict";
var D = null, tab = "tab", cat = null, cview = "circolo";
function esc(s){return String(s==null?"":s).replace(/[&<>"']/g,function(c){return {"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c];});}
function nm(id){return (D.nomi && D.nomi[id]) || "";}
function rkOf(id){return (D.rank && D.rank[id]) || "";}
var RN = {2:"Finale",4:"Semifinali",8:"Quarti di finale",16:"Ottavi di finale",32:"Sedicesimi",64:"Trentaduesimi"};
function fmtLabel(f){return f==="stb"?"2 set + tie-break a 10":"al meglio dei 3 set";}

function plHtml(id, m, d, first){
  if (id === -1) return '<div class="pl bye"><span>BYE</span></div>';
  if (!id || id < 0 || !nm(id)) return '<div class="pl tbd"><span>da definire</span></div>';
  var cls = m.w === id ? "win" : (m.w > 0 ? "lose" : "");
  var sd = d.seeds && d.seeds[id] ? '<span class="sd">' + d.seeds[id] + '</span>' : "";
  return '<div class="pl ' + cls + '"><span>' + sd + esc(nm(id)) + '</span><span class="rk">' + (first ? esc(rkOf(id)) : "") + '</span></div>';
}
function bracketHtml(c){
  var d = c.draw, h = '<div class="card"><h2>' + esc(c.nome) + '</h2><div class="muted" style="margin-bottom:8px">' + fmtLabel(d.fmt) + '</div><div class="bracket">';
  d.rounds.forEach(function(rd, r){
    h += '<div class="round"><h4>' + (RN[d.size >> r] || ("Turno da " + (d.size >> r))) + '</h4><div class="matches">';
    rd.forEach(function(m){
      var real = m.a > 0 && m.b > 0;
      h += '<div class="match">' + plHtml(m.a, m, d, r === 0) + plHtml(m.b, m, d, r === 0) + (real && m.score ? '<div class="sc">' + esc(m.score) + '</div>' : "") + '</div>';
    });
    h += '</div></div>';
  });
  h += '</div>';
  var fin = d.rounds[d.rounds.length - 1][0];
  if (fin.w > 0 && nm(fin.w)) h += '<div class="champ">&#127942; Vincitore: ' + esc(nm(fin.w)) + '</div>';
  return h + '</div>';
}
function rrHtml(c){
  var rr = c.rr, h = '<div class="card"><h2>' + esc(c.nome) + ' - girone all\'italiana</h2><div class="muted">' + fmtLabel(rr.fmt) + '. Vittoria 2 punti, sconfitta 1, W.O. 0.</div></div>';
  rr.groups.forEach(function(g, gi){
    var st = (c.stand && c.stand[gi]) || [];
    h += '<div class="card"><h2>Girone ' + String.fromCharCode(65 + gi) + '</h2><div class="st"><span class="h">#</span><span class="h">Giocatore</span><span class="h">V</span><span class="h">P</span><span class="h">Set</span><span class="h">Pt</span>';
    st.forEach(function(s){
      h += '<span>' + s.pos + '</span><span class="n">' + esc(nm(s.id)) + '</span><span>' + s.v + '</span><span>' + s.p + '</span><span>' + s.sv + '-' + s.sp + '</span><b>' + s.pt + '</b>';
    });
    h += '</div><div style="margin-top:10px">';
    rr.matches.forEach(function(m){
      if (m.g !== gi) return;
      var a = m.w === m.a ? '<b>' + esc(nm(m.a)) + '</b>' : esc(nm(m.a));
      var b = m.w === m.b ? '<b>' + esc(nm(m.b)) + '</b>' : esc(nm(m.b));
      h += '<div class="rrm"><span class="t">T' + m.r + '</span><span>' + a + ' - ' + b + '</span><span class="muted">' + (m.w > 0 ? esc(m.score) : "da giocare") + '</span></div>';
    });
    h += '</div></div>';
  });
  return h;
}
function circoloHtml(){
  var list = cview === "interna" ? D.interna : D.circolo;
  var h = '<div class="card"><h2>Classifica di circolo</h2><div class="chips" style="padding:0 0 8px">' +
    '<button class="' + (cview === "circolo" ? "on" : "") + '" data-cv="circolo">Tutti</button>' +
    '<button class="' + (cview === "interna" ? "on" : "") + '" data-cv="interna">Non agonisti (4.NC)</button></div>' +
    '<div class="muted">Punti degli ultimi ' + D.mesi + ' mesi (a destra il totale).</div>';
  if (!list || !list.length) return h + '<p class="muted">Nessun risultato ancora.</p></div>';
  list.forEach(function(r){
    h += '<div class="crow"><span class="pos">' + r.pos + '</span><span>' + esc(r.nome) + ' <span class="muted">' + esc(r.class) + '</span></span>' +
      '<span class="cp"><b>' + r.pt12.toFixed(1) + '</b><small>' + r.ptTot.toFixed(1) + ' tot</small></span>' +
      '<span class="cs">' + r.g + ' giocate, ' + r.v + ' vinte, ' + r.p + ' perse - set ' + r.sv + '-' + r.sp + '</span></div>';
  });
  return h + '</div>';
}
function draw(){
  if (!D) return;
  document.title = D.title || "Torneo";
  document.getElementById("ttl").firstChild.nodeValue = D.title || "Torneo";
  document.getElementById("sub").textContent = [D.luogo, D.date].filter(Boolean).join(" - ");
  document.getElementById("tabs").innerHTML =
    '<button class="' + (tab === "tab" ? "on" : "") + '" data-tab="tab">Tabelloni</button>' +
    '<button class="' + (tab === "circ" ? "on" : "") + '" data-tab="circ">Classifica di circolo</button>';
  var chips = document.getElementById("chips"), main = document.getElementById("main");
  if (tab === "circ") {
    chips.innerHTML = "";
    main.innerHTML = circoloHtml();
  } else {
    var cats = D.cats || [];
    if (!cats.length) { chips.innerHTML = ""; main.innerHTML = '<div class="card muted">I tabelloni non sono ancora stati pubblicati.</div>'; }
    else {
      if (!cats.some(function(c){return c.key === cat;})) cat = cats[0].key;
      chips.innerHTML = cats.map(function(c){return '<button class="' + (c.key === cat ? "on" : "") + '" data-cat="' + c.key + '">' + esc(c.nome) + '</button>';}).join("");
      var c = cats.filter(function(x){return x.key === cat;})[0];
      main.innerHTML = c.draw ? bracketHtml(c) : rrHtml(c);
    }
  }
  main.insertAdjacentHTML("beforeend", '<div class="foot">Aggiornato alle ' + esc(D.aggiornato) + ' - la pagina si aggiorna da sola</div>');
}
document.addEventListener("click", function(e){
  var t = e.target.closest("button"); if (!t) return;
  if (t.dataset.tab) { tab = t.dataset.tab; draw(); }
  else if (t.dataset.cat) { cat = t.dataset.cat; draw(); }
  else if (t.dataset.cv) { cview = t.dataset.cv; draw(); }
});
function load(){
  fetch("/api/pub", {cache: "no-store"}).then(function(r){return r.json();}).then(function(j){
    var sx = window.scrollX, sy = window.scrollY, br = document.querySelector(".bracket"), bx = br ? br.scrollLeft : 0;
    D = j; draw();
    window.scrollTo(sx, sy);
    var b2 = document.querySelector(".bracket"); if (b2) b2.scrollLeft = bx;
  }).catch(function(){});
}
load(); setInterval(load, 30000);
</script>
</body>
</html>
`
