import { Component, ElementRef, Inject, OnDestroy, ViewChild, Renderer2, HostListener, AfterViewInit } from '@angular/core';
import { Subscription } from 'rxjs';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { TranslateService } from '@ngx-translate/core';

import { ApiService } from '../../../../../services/api.service';
import { AppConfig } from 'src/app/app.config';
import { OperationError, OperationErrorTypes } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';

/**
 * Const for accessing the code on src/assets/scripts/terminal.js. It allows to create a terminal
 * emulator. The original code is from https://github.com/eosterberg/terminaljs, but the version
 * used in this app has several modifications.
 */
declare const Terminal: any;

/**
 * Data needed for BasicTerminalComponent to works.
 */
export interface BasicTerminalData {
  /**
   * Public key of the node.
   */
  pk: string;
  /**
   * Node label.
   */
  label: string;
}

/**
 * Modal window used as a terminal emulator for controlling the nodes. It just gets the user
 * input, sends it to the hypervisor via the API and prints the response.
 */
@Component({
  selector: 'app-basic-terminal',
  templateUrl: './basic-terminal.component.html',
  styleUrls: ['./basic-terminal.component.scss']
})
export class BasicTerminalComponent implements AfterViewInit, OnDestroy {
  @ViewChild('terminal') terminalElement: ElementRef<HTMLDivElement>;
  @ViewChild('dialogContent') dialogContentElement: ElementRef<HTMLDivElement>;
  private terminal: any;
  private subscription: Subscription;

  // These variables store the history of the commands sent by the user, to make it possible to
  // Use the keyboard arrows to call old commands again.
  private history: string[] = [];
  private historyIndex = 0;
  private currentInputText = '';

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, data: BasicTerminalData): MatDialogRef<BasicTerminalComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;

    return dialog.open(BasicTerminalComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: BasicTerminalData,
    public dialogRef: MatDialogRef<BasicTerminalComponent>,
    private renderer: Renderer2,
    private apiService: ApiService,
    private translate: TranslateService,
  ) { }

  ngAfterViewInit() {
    // Create the terminal.
    this.terminal = new Terminal(null);
    this.terminal.setWidth('100%');
    this.terminal.setBackgroundColor('black');
    this.terminal.setTextSize('15px');
    this.terminal.blinkingCursor(true);
    // Add it to the DOM.
    this.renderer.appendChild(this.terminalElement.nativeElement, this.terminal.html);

    this.waitForInput();
  }

  ngOnDestroy() {
    if (this.subscription) {
      this.subscription.unsubscribe();
    }
  }

  // Check the keyboard to be able to restore old commands by using the arrow keys.
  @HostListener('window:keyup', ['$event'])
  keyEvent(event: KeyboardEvent) {
    if (this.terminal.hasFocus() && this.history.length > 0) {

      // Up arrow.
      if (event.keyCode === 38) {
        if (this.historyIndex === this.history.length) {
          // Save the currently entered text.
          this.currentInputText = this.terminal.getInputContent();
        }

        this.historyIndex = this.historyIndex > 0 ? this.historyIndex - 1 : 0;
        this.terminal.changeInputContent(this.history[this.historyIndex]);
      }

      // Down arrow
      if (event.keyCode === 40) {
        this.historyIndex = this.historyIndex < this.history.length ? this.historyIndex + 1 : this.history.length;
        if (this.historyIndex !== this.history.length) {
          this.terminal.changeInputContent(this.history[this.historyIndex]);
        } else {
          // Restore the text the user was entering.
          this.terminal.changeInputContent(this.currentInputText);
        }
      }
    }
  }

  focusTerminal() {
    this.terminal.html.click();
  }

  private waitForInput() {
    // Print the header string and wait for user input.
    this.terminal.input(this.translate.instant('actions.terminal.input-start', { address: this.data.pk }), (input) => {
      // Save the command in the history and go to the end of the history.
      this.history.push(input);
      this.historyIndex = this.history.length;
      this.currentInputText = '';

      // Send the command and wait for the response of the hypervisor.
      this.subscription = this.apiService.post(`/visors/${this.data.pk}/exec`, { command: input })
      .subscribe(response => {
        // Print the response.
        if (response.output) {
          this.printLines(response.output);
        } else {
          this.printLines(this.translate.instant('actions.terminal.error'));
        }

        this.printLines(' ');
        this.waitForInput();
      }, (error: OperationError) => {
        error = processServiceError(error);

        if (error.originalServerErrorMsg && typeof error.originalServerErrorMsg === 'string') {
          if (error.type === OperationErrorTypes.Unknown) {
            this.printLines(error.originalServerErrorMsg);
          } else {
            this.printLines(this.translate.instant(error.translatableErrorMsg));
          }
        } else {
          this.printLines(this.translate.instant('actions.terminal.error'));
        }

        this.printLines(' ');
        this.waitForInput();
      });
    });
  }

  /**
   * Process the response returned by the backend, to adapt it to the format expected by
   * the terminal emulator, and them prints it. It also moves the scroll to the last line.
   */
  private printLines(text: string) {
    let processedText = text.replace(/</g, '&lt;');
    processedText = processedText.replace(/>/g, '&gt;');
    processedText = processedText.replace(/\n/g, '<br/>');
    processedText = processedText.replace(/\t/g, '&emsp;');
    processedText = processedText.replace(/ /g, '&nbsp;');

    this.terminal.print(processedText);

    setTimeout(() => {
      this.dialogContentElement.nativeElement.scrollTop = this.dialogContentElement.nativeElement.scrollHeight;
    });
  }
}
