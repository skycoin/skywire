import { TranslateLoader, TranslateModule } from '@ngx-translate/core';
import { from, Observable } from 'rxjs';
import { NgModule } from '@angular/core';

export class TranslationModuleLoader implements TranslateLoader {
  getTranslation(lang: string): Observable<any> {
    // Fetch the translation JSON as a runtime ASSET rather than a dynamic import().
    // A dynamic import() becomes a lazy webpack chunk resolved relative to the
    // module — which the single-file serverless build (cli hv gen, opened from
    // file://) neither inlines nor can load (module loading is blocked on file://),
    // leaving the UI showing raw keys like "nodes.title". fetch() of the asset path
    // is served by the server in the visor-served build and by override.js's inlined
    // __SKYWIRE_ASSETS__ shim in the single-file build — so it works in both.
    return from(fetch(`assets/i18n/${lang}.json`).then(r => r.json()));
  }
}

@NgModule({
  imports: [TranslateModule.forRoot({
    loader: {
      provide: TranslateLoader,
      useClass: TranslationModuleLoader,
    },
  })],
  exports: [TranslateModule],
})
export class AppTranslationModule { }
