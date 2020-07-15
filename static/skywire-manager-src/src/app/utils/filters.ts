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
