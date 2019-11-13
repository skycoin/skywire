import { Injectable } from '@angular/core';
import { Observable, Subject } from 'rxjs';

export interface SidenavOption {
  name: string;
  icon: string;
  actionName: string;
  disabled?: boolean;
}

export interface SidenavContents {
  upperContents: SidenavOption[];
  lowerContents: SidenavOption[];
}

@Injectable({
  providedIn: 'root'
})
export class SidenavService {
  private upperContentsInternal: SidenavOption[];
  private lowerContentsInternal: SidenavOption[];
  private actionsSubject: Subject<string>;

  get upperContents(): SidenavOption[] {
    return this.upperContentsInternal;
  }
  get lowerContents(): SidenavOption[] {
    return this.lowerContentsInternal;
  }

  setContents(upperContents: SidenavOption[], lowerContents: SidenavOption[]): Observable<string> {
    if (this.actionsSubject) {
      this.actionsSubject.complete();
    }

    this.upperContentsInternal = upperContents;
    this.lowerContentsInternal = lowerContents;

    this.actionsSubject = new Subject<string>();
    return this.actionsSubject.asObservable();
  }

  requestAction(name: string) {
    if (this.actionsSubject) {
      this.actionsSubject.next(name);
    }
  }
}
