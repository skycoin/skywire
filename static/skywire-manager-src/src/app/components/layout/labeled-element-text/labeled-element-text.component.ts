import { Component, Input, Output, EventEmitter, OnDestroy } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';

import { SelectOptionComponent, SelectableOption } from '../select-option/select-option.component';
import { StorageService, LabelInfo, LabeledElementTypes } from 'src/app/services/storage.service';
import { ClipboardService } from 'src/app/services/clipboard.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { EditLabelComponent } from '../edit-label/edit-label.component';
import GeneralUtils from 'src/app/utils/generalUtils';
import { TranslateService } from '@ngx-translate/core';

/**
 * Represents the parts of a label.
 */
export class LabelComponents {
  /**
   * Prefix shown at the start of the label, mainly for identifying local nodes.
   * The text is a var for the translate pipe.
   */
  prefix = '';
  /**
   * Text for separating the prefix from the label.
   */
  prefixSeparator = '';
  /**
   * Element label, to be shown without using the translate pipe.
   */
  label = '';
  /**
   * Element label, to be shown using the translate pipe.
   */
  translatableLabel = '';
  /**
   * Original saved label info.
   */
  labelInfo: LabelInfo;
}

/**
 * Shows the id of an element and a label identifying it. An icon is shown at the end of the text,
 * to indicate the user that the text can be copied by clicking for showing options. This
 * component allows to change the label and to copy the id. This component can show truncated
 * text for the id, case in which a tooltip allows the user to see the complete id.
 */
@Component({
  selector: 'app-labeled-element-text',
  templateUrl: './labeled-element-text.component.html',
  styleUrls: ['./labeled-element-text.component.scss']
})
export class LabeledElementTextComponent implements OnDestroy {
  private idInternal: string;
  /**
   * Id of the element to show.
   */
  @Input() set id(val: string) {
    this.idInternal = val;

    this.labelComponents = LabeledElementTextComponent.getLabelComponents(this.storageService, this.id);
  }
  get id(): string { return this.idInternal ? this.idInternal : ''; }

  /**
   * Indicates if the text with the id must be truncated if it is too long.
   */
  @Input() public short = false;
  /**
   * Number of characters at the left and right of the id that will be shown if "short" is
   * "true". Example: if the id is "123456789" and this var is set to 2, the component will
   * show "12...89". If the id has a length less than shortTextLength * 2, the whole id
   * is shown.
   */
  @Input() shortTextLength = 5;
  /**
   * Type of the element to which the id corresponds.
   */
  @Input() elementType: LabeledElementTypes = LabeledElementTypes.Node;

  /**
   * Event for when the label is changed.
   */
  @Output() labelEdited = new EventEmitter();
  /**
   * Parts of the label to be shown.
   */
  labelComponents: LabelComponents;

  /**
   * Gets the parts which form the label shown by this component for a particular ID.
   * @param id Id to check.
   */
  private static getLabelComponents(storageService: StorageService, id: string): LabelComponents {
    // Detect if the id if for a local node.
    let isLocalNode: boolean;
    if (storageService.getSavedVisibleLocalNodes().has(id)) {
      isLocalNode = true;
    } else {
      isLocalNode = false;
    }

    const response = new LabelComponents();

    // Get the label associated to the id.
    response.labelInfo = storageService.getLabelInfo(id);
    if (response.labelInfo && response.labelInfo.label) {
      // If the ID is for a local node, add a prefix indicating that.
      if (isLocalNode) {
        response.prefix = 'labeled-element.local-element';
        response.prefixSeparator = ' - ';
      }

      response.label = response.labelInfo.label;
    } else {
      // Add a default text.
      if (storageService.getSavedVisibleLocalNodes().has(id)) {
        response.prefix = 'labeled-element.unnamed-local-visor';
      } else {
        response.translatableLabel = 'labeled-element.unnamed-element';
      }
    }

    return response;
  }

  /**
   * Allows to get a string whith the label for an id as it would be shown by this component.
   * @param id Id to check.
   */
  public static getCompleteLabel(storageService: StorageService, translateService: TranslateService, id: string): string {
    const labelElements = LabeledElementTextComponent.getLabelComponents(storageService, id);

    // Build the string.
    return (labelElements.prefix ? translateService.instant(labelElements.prefix) : '') +
      labelElements.prefixSeparator +
      labelElements.label +
      (labelElements.translatableLabel ? translateService.instant(labelElements.translatableLabel) : '');
  }

  constructor(
    private dialog: MatDialog,
    private storageService: StorageService,
    private clipboardService: ClipboardService,
    private snackbarService: SnackbarService,
  ) { }

  ngOnDestroy() {
    this.labelEdited.complete();
  }

  processClick() {
    // Options for the options modal window.
    const options: SelectableOption[] = [
      {
        icon: 'filter_none',
        label: 'labeled-element.copy',
      },
      {
        icon: 'edit',
        label: 'labeled-element.edit-label',
      }
    ];

    if (this.labelComponents.labelInfo) {
      options.push({
        icon: 'close',
        label: 'labeled-element.remove-label',
      });
    }

    // Show the options modal window.
    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        // Copy the id.
        if (this.clipboardService.copy(this.id)) {
          this.snackbarService.showDone('copy.copied');
        }
      } else if (selectedOption === 3) {
        // Ask for confirmation and remove the label.
        const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'labeled-element.remove-label-confirmation');

        confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
          confirmationDialog.componentInstance.closeModal();

          this.storageService.saveLabel(this.id, null, this.elementType);
          this.snackbarService.showDone('edit-label.label-removed-warning');

          this.labelEdited.emit();
        });
      } else {
        // Params for the edit label modal window.
        if (selectedOption === 2) {
          let labelInfo =  this.labelComponents.labelInfo;
          if (!labelInfo) {
            labelInfo = {
              id: this.id,
              label: '',
              identifiedElementType: this.elementType,
            };
          }

          // Show the edit label modal window.
          EditLabelComponent.openDialog(this.dialog, labelInfo).afterClosed().subscribe((changed: boolean) => {
            if (changed) {
              this.labelEdited.emit();
            }
          });
        }
      }
    });
  }
}
