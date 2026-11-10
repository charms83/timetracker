const { contextBridge, ipcRenderer } = require("electron");
contextBridge.exposeInMainWorld("zpTab", {
  toggle:    () => ipcRenderer.send("tab:toggle"),
  onPeek:    cb => ipcRenderer.on("tab:peek", (_, r) => cb(r)),
  onUpdate:  cb => ipcRenderer.on("tab:update", (_, s) => cb(s)),
  onState:   cb => ipcRenderer.on("tab:state", (_, e) => cb(e)),
  onSide:    cb => ipcRenderer.on("tab:side", (_, s) => cb(s)),
  dragStart: () => ipcRenderer.send("tab:dragstart"),
  drag:      (dx, dy) => ipcRenderer.send("tab:drag", { dx, dy }),
  dragEnd:   () => ipcRenderer.send("tab:dragend"),
});
