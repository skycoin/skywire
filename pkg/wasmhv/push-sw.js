// push-sw.js shows the visor alerts a hypervisor sends by Web Push
// (pkg/alerts/push.go). It is served alone as /push-sw.js for the dashboard
// and appended to sw.js, so a page that already has a root worker gets it too.

self.addEventListener('push', (event) => {
  let d = {};
  try { d = event.data ? event.data.json() : {}; } catch (e) { d = { body: event.data ? event.data.text() : '' }; }
  const title = d.title || 'Skywire alert';
  event.waitUntil(self.registration.showNotification(title, {
    body: d.body || '',
    tag: d.tag || 'skywire',
    renotify: true,
    icon: 'icon-192.png',
    data: d,
  }));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const scope = self.registration.scope;
  event.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((list) => {
    for (const c of list) {
      if (c.url.startsWith(scope) && 'focus' in c) { return c.focus(); }
    }
    return self.clients.openWindow(scope);
  }));
});
