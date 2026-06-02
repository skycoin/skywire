import { enableProdMode, provideZoneChangeDetection } from '@angular/core';
import { platformBrowserDynamic } from '@angular/platform-browser-dynamic';

import { AppModule } from './app/app.module';
import { environment } from './environments/environment';
import { installClientErrorReporter } from './app/services/client-error-reporter';

if (environment.production) {
  enableProdMode();
}

// Forward browser errors (window.onerror / unhandled rejections /
// console.error+warn) to the visor log via POST /api/client-log.
// Installed before bootstrap so it also captures bootstrap failures.
installClientErrorReporter();

platformBrowserDynamic().bootstrapModule(AppModule, { applicationProviders: [provideZoneChangeDetection()], })
  .catch(err => console.error(err));
