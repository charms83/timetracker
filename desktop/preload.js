const { contextBridge, ipcRenderer } = require("electron");
contextBridge.exposeInMainWorld("zp", {
  hide:     () => ipcRenderer.send("mini:hide"),
  quit:     () => ipcRenderer.send("mini:quit"),
  openMain: (view) => ipcRenderer.send("mini:openMain", view),
  changed:  () => ipcRenderer.send("mini:changed"),
  meetingTimerStarted: () => ipcRenderer.send("meeting:timerStarted"),
  onReminder: cb => ipcRenderer.on("reminder", (_, r) => cb(r)),
  onRefresh:  cb => ipcRenderer.on("refresh", () => cb()),
  browserTickets: () => ipcRenderer.invoke("browser:tickets"),
});
