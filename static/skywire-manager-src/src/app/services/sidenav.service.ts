import { Injectable } from '@angular/core';
import { Observable, Subject } from 'rxjs';

/**
 * Option to be shown in the left bar.
 */
export interface SidenavOption {
  /**
   * Text that will be shown in the button.
   */
  name: string;
  /**
   * Icon that will be shown in the button.
   */
  icon: string;
  /**
   * Unique string to identify the option if the user selects it.
   */
  actionName: string;
  disabled?: boolean;
}

/**
 * Allows interact with the options bar that is shown at the left in most parts of the app.
 */
@Injectable({
  providedIn: 'root'
})
export class SidenavService {
  private upperContentsInternal: SidenavOption[];
  private lowerContentsInternal: SidenavOption[];
  /**
   * Subject for informing when the user clicks an option.
   */
  private actionsSubject: Subject<string>;

  get upperContents(): SidenavOption[] {
    return this.upperContentsInternal;
  }
  get lowerContents(): SidenavOption[] {
    return this.lowerContentsInternal;
  }

  /**
   * Sets the options that will be shown in the left bar until replaced by a new call to this function.
   * It returns an observable that informs when the user clicks any of the options, by returning the
   * value of the "actionName" property of the clicked option.
   */
  setContents(upperContents: SidenavOption[], lowerContents: SidenavOption[]): Observable<string> {
    if (this.actionsSubject) {
      this.actionsSubject.complete();
    }

    this.upperContentsInternal = upperContents;
    this.lowerContentsInternal = lowerContents;

    this.actionsSubject = new Subject<string>();
    return this.actionsSubject.asObservable();
  }

  /**
   * Informs that the user clicked an option.
   * @param name Value of the "actionName" property of the clicked option.
   */
  requestAction(name: string) {
    if (this.actionsSubject) {
      this.actionsSubject.next(name);
    }
  }
}
