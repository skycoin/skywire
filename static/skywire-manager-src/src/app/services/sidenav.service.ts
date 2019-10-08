import { Injectable, TemplateRef } from '@angular/core';
import { Observable, ReplaySubject } from 'rxjs';

@Injectable({
  providedIn: 'root'
})
export class SidenavService {
  private template = new ReplaySubject<TemplateRef<any>>(1);

  getTemplate(): Observable<TemplateRef<any>> {
    return this.template.asObservable();
  }

  setTemplate(template: TemplateRef<any>) {
    this.template.next(template);
  }
}
