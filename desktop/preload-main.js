// Hauptfenster: nur das Nötigste aus der Mac-App bereitstellen
const { contextBridge, ipcRenderer } = require("electron");
contextBridge.exposeInMainWorld("zpDesktop", {
  browserTickets: () => ipcRenderer.invoke("browser:tickets"),   // Ticket im aktiven Browser-Tab (Jira/Plane)
  browsers: () => ipcRenderer.invoke("ext:browsers"),             // installierte Browser
  installAddon: name => ipcRenderer.invoke("ext:install", name),  // Add-on anbieten
  installAll: () => ipcRenderer.invoke("ext:installAll"),        // alle gefundenen Browser
});
