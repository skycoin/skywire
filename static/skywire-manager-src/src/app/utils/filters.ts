/**
 * Contains the properties needed for showing the selected filters in the UI.
 * For the UI to work well, at least one of the value properties must have a value.
 */
export interface FilterTextElements {
  /**
   * String for the translate pipe with the name of the filtered element.
   */
  filterName: string;
  /**
   * Value selected by the user to be used as filter, if it is not a var for the translate pipe.
   */
  value?: string;
  /**
   * Value selected by the user to be used as filter, if it is a var for the translate pipe.
   */
  translatableValue?: string;
}

/**
 * Field types the UI can show for the filters.
 */
export enum FilterFieldTypes {
  /**
   * Field in which the user can enter text freely. When using this type, the maxlength property
   * of the FilterProperties object must have a value.
   */
  TextInput = 'TextInput',
  /**
   * Field in which the user must select the value from a list. When using this type, the option
   * list will be created using the printableLabelsForValues property list of the
   * FilterProperties object.
   */
  Select = 'Select',
}

/**
 * Class with the basic properties about a filter that can be applied to a data array.
 */
export interface FilterProperties {
  /**
   * String to be shown in the UI using the translate pipe, identifying the filter.
   */
  filterName: string;
  /**
   * List with all posible values the property may have and the corresponding translatable var
   * that must be shown in the UI. Only useful for properties with a limited number of
   * posible values.
   */
  printableLabelsForValues?: PrintableLabel[];
  /**
   * Max allowed length for the filter, if the field is a text input.
   */
  maxlength?: number;
  /**
   * Type of the field to be shown in the filtering form.
   */
  type: FilterFieldTypes;
  /**
   * Name of the property in the data list which is going to be filtered.
   */
  keyNameInElementsArray: string;
  /**
   * Name of an additional property in the data list which is going to be filtered.
   * This allows to compare a filter with more than one property.
   */
  secondaryKeyNameInElementsArray?: string;
  /**
   * General configuration settings indication how the PrintableLabel elements must be shown
   * in the UI, if any. If no special configuration is needed, there is no need to set it.
   */
  printableLabelGeneralSettings?: PrintableLabelGeneralSettings;
}

/**
 * Represents a possible value of a property. It allows to separate the actual value of the
 * property and the text that will be shown in the UI.
 */
export interface PrintableLabel {
  /**
   * Actual value.
   */
  value: string;
  /**
   * Value to be shown in the UI. Preferably a var for the translate pipe.
   */
  label: string;
  /**
   * URL of the image to show with the label. For it to work, the parent FilterProperties element
   * must have a valid PrintableLabelGeneralSettings object. If not set, no image is shown.
   */
  image?: string;
}

/**
 * General configuration settings indication how the PrintableLabel elements must be shown
 * in the UI.
 */
export interface PrintableLabelGeneralSettings {
  /**
   * Witdth, in px, of the images that will be shown with the labels in the UI. If any
   * PrintableLabel element has a value in the image property, this property must have
   * a valid value.
   */
  imageWidth: number;
  /**
   * Heigth, in px, of the images that will be shown with the labels in the UI. If any
   * PrintableLabel element has a value in the image property, this property must have
   * a valid value.
   */
  imageHeight: number;
  /**
   * Default image to show if the image set in a PrintableLabel element is not found.
   */
  defaultImage: string;
}

/**
 * Filter properties with added information about were to find the current filter in the
 * filters object.
 */
export interface CompleteFilterProperties extends FilterProperties {
  /**
   * Name of the property in the object with the current filters.
   */
  keyNameInFiltersObject: string;
}

/**
 * Filters a list and returns the result.
 * @param allElements Element list to be filtered.
 * @param currentFilters Object with the filters to apply. Filters with empty strings and null as
 * values are ignored.
 * @param filterProperties Objects with the info for associating the filters objects with the
 * elements of the data list.
 */
export function filterList(allElements: any[], currentFilters: any, filterPropertiesList: CompleteFilterProperties[]): any[] {
  if (allElements) {
    let response: any[];

    // Check which filters are valid and create an array including only the properties for
    // those filters.
    const cleanedFilterPropertiesList: CompleteFilterProperties[] = [];
    Object.keys(currentFilters).forEach(key => {
      if (currentFilters[key]) {
        for (let i = 0; i < filterPropertiesList.length; i++) {
          if (filterPropertiesList[i].keyNameInFiltersObject === key) {
            cleanedFilterPropertiesList.push(filterPropertiesList[i]);
            break;
          }
        }
      }
    });

    // Filter the elements.
    response = allElements.filter(element => {
      let valid = true;

      // Check if the element pass all the filters.
      cleanedFilterPropertiesList.forEach(filterProperties => {
        const primaryPropertyValid = String(element[filterProperties.keyNameInElementsArray]).toLowerCase().includes(
          currentFilters[filterProperties.keyNameInFiltersObject].toLowerCase());

        const secondaryPropertyValid = filterProperties.secondaryKeyNameInElementsArray &&
          String(element[filterProperties.secondaryKeyNameInElementsArray])
          .toLowerCase().includes(currentFilters[filterProperties.keyNameInFiltersObject].toLowerCase());

        if (!primaryPropertyValid && !secondaryPropertyValid) {
          valid = false;
        }
      });

      return valid;
    });

    return response;
  }

  return null;
}

/**
 * Creates the objects for showing the list of selected filters in the UI. If there are no
 * valid filters, the returned list is empty.
 * @param currentFilters Object with the current filters. Filters with empty strings and null as
 * values are ignored.
 * @param filterPropertiesList Objects with the info for associating the filters objects with the
 * elements of the data list.
 */
export function updateFilterTexts(currentFilters: any, filterPropertiesList: CompleteFilterProperties[]): FilterTextElements[] {
  const response = [];

  filterPropertiesList.forEach(filterProperties => {
    // Check if the filter has a valid value.
    if (currentFilters[filterProperties.keyNameInFiltersObject]) {
      let value: string;
      let translatableValue: string;

      // Check if there is a translatable var for the current value.
      if (filterProperties.printableLabelsForValues) {
        filterProperties.printableLabelsForValues.forEach(printableLabel => {
          if (printableLabel.value === currentFilters[filterProperties.keyNameInFiltersObject]) {
            translatableValue = printableLabel.label;
          }
        });
      }
      if (!translatableValue) {
        value = currentFilters[filterProperties.keyNameInFiltersObject];
      }

      // Add the data to the list.
      response.push({
        filterName: filterProperties.filterName,
        translatableValue: translatableValue,
        value: value,
      });
    }
  });

  return response;
}
