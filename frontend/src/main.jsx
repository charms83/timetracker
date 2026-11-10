import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App.jsx";
import { bootServerMode } from "./zp-ui.jsx";
import "@fontsource/quantico/400.css";   // Beispiel-Hausschrift für Produktname und große Überschriften (SIL OFL)
import "./index.css";
// Servermodus (Go-Server) einmal vor dem ersten Rendern erkennen; am Node-System ist das ein einzelner 404 ohne Folgen.
// true = Weiterleitung läuft (Anmeldung bzw. /admin), dann nichts rendern.
bootServerMode().catch(() => false).then(redirecting => { if (!redirecting) ReactDOM.createRoot(document.getElementById("root")).render(<App/>); });
