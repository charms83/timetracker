// JXA (osascript -l JavaScript): liest Termine aus dem macOS-Kalender über EventKit.
// Fragt beim ersten Mal nach Kalenderzugriff (Dialog von macOS). Ausgabe: JSON.
ObjC.import("EventKit");
ObjC.import("Foundation");
function run(argv) {
  const back = +argv[0] || 6, fwd = +argv[1] || 1;
  const status = () => Number(ObjC.unwrap($.EKEventStore.authorizationStatusForEntityType(0)));   // 0 = Termine
  let st = status();
  if (st === 0) {                                                            // noch nie gefragt → Dialog zeigen
    let done = false;
    const req = $.EKEventStore.alloc.init;
    const cb = function () { done = true; };
    if (req.respondsToSelector("requestFullAccessToEventsWithCompletion:")) req.requestFullAccessToEventsWithCompletion(cb);
    else req.requestAccessToEntityTypeCompletion(0, cb);
    const until = Date.now() + 90000;
    while (!done && Date.now() < until) $.NSRunLoop.currentRunLoop.runUntilDate($.NSDate.dateWithTimeIntervalSinceNow(0.3));
    st = status();
  }
  if (st !== 3) return JSON.stringify({ error: "noaccess", status: st });   // 1 eingeschränkt, 2 verweigert, 4 nur schreiben
  const store = $.EKEventStore.alloc.init;
  const cals = store.calendarsForEntityType(0);
  const from = $.NSDate.dateWithTimeIntervalSinceNow(-back * 86400), to = $.NSDate.dateWithTimeIntervalSinceNow(fwd * 86400);
  const arr = store.eventsMatchingPredicate(store.predicateForEventsWithStartDateEndDateCalendars(from, to, cals));
  const out = [];
  const str = v => { try { const s = ObjC.unwrap(v); return s == null ? null : String(s); } catch (_) { return null; } };
  for (let i = 0; i < arr.count; i++) {
    const ev = arr.objectAtIndex(i);
    out.push({ title: str(ev.title) || "Termin", calendar: str(ev.calendar.title), location: str(ev.location), allDay: !!ev.allDay,
      start: new Date(ev.startDate.timeIntervalSince1970 * 1000).toISOString(), end: new Date(ev.endDate.timeIntervalSince1970 * 1000).toISOString() });
  }
  return JSON.stringify({ events: out, calendars: cals.count });
}
