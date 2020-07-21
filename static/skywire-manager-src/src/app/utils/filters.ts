import { PrintableLabel } from './generalUtils';

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
 * Class with data for associating a filter with a property of the data to be filtered.
 */
export interface FilterKeysAssociation {
  /**
   * String to be shown in the UI using the translate pipe, with the name of the filter.
   */
  filterName: string;
  /**
   * List with all posible values the property may have and the corresponding translatable var
   * that must be shown in the UI. Only useful for properties with a limited number of
   * posible values.
   */
  printableLabelsForValues?: PrintableLabel[];
  /**
   * Name of the property in the elements of the list which is going to be filtered.
   */
  keyNameInElementsArray: string;
  /**
   * Name of an additional property in the elements of the list which is going to be filtered.
   * This allows to compare a filter with more than one property.
   */
  secondaryKeyNameInElementsArray?: string;
  /**
   * Name of the property in the object with the filters.
   */
  keyNameInFiltersObject: string;
}

/**
 * Filters a list and returns the result.
 * @param allElements Element list to be filtered.
 * @param currentFilters Object with the filters to apply. Filters with empty strings and null as
 * values are ignored.
 * @param filterKeysAssociations Object with the info for associating the filters object with the
 * elements of the list.
 */
export function filterList(allElements: any[], currentFilters: any, filterKeysAssociations: FilterKeysAssociation[]): any[] {
  if (allElements) {
    let response: any[];

    // Check which filters are valid and create an array including only the associations for
    // those filters.
    const cleanedFilterKeysAssociations: FilterKeysAssociation[] = [];
    Object.keys(currentFilters).forEach(key => {
      if (currentFilters[key]) {
        for (let i = 0; i < filterKeysAssociations.length; i++) {
          if (filterKeysAssociations[i].keyNameInFiltersObject === key) {
            cleanedFilterKeysAssociations.push(filterKeysAssociations[i]);
            break;
          }
        }
      }
    });

    // Filter the elements.
    response = allElements.filter(element => {
      let valid = true;

      // Check if the element pass all the filters.
      cleanedFilterKeysAssociations.forEach(association => {
        const primaryPropertyValid = String(element[association.keyNameInElementsArray]).toLowerCase().includes(
          currentFilters[association.keyNameInFiltersObject].toLowerCase());

        const secondaryPropertyValid = association.secondaryKeyNameInElementsArray &&
          String(element[association.secondaryKeyNameInElementsArray])
          .toLowerCase().includes(currentFilters[association.keyNameInFiltersObject].toLowerCase());

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
 * @param filterKeysAssociations Object with the info for associating the filters object with the
 * data that must be shown in the UI.
 */
export function updateFilterTexts(currentFilters: any, filterKeysAssociations: FilterKeysAssociation[]): FilterTextElements[] {
  const response = [];

  filterKeysAssociations.forEach(association => {
    // Check if the filter has a valid value.
    if (currentFilters[association.keyNameInFiltersObject]) {
      let value: string;
      let translatableValue: string;

      // Check if there is a translatable value for the current value.
      if (association.printableLabelsForValues) {
        association.printableLabelsForValues.forEach(printableLabel => {
          if (printableLabel.value === currentFilters[association.keyNameInFiltersObject]) {
            translatableValue = printableLabel.label;
          }
        });
      }
      if (!translatableValue) {
        value = currentFilters[association.keyNameInFiltersObject];
      }

      // Add the data to the list.
      response.push({
        filterName: association.filterName,
        translatableValue: translatableValue,
        value: value,
      });
    }
  });

  return response;
}
