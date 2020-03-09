// NOTE: originally from the desktop wallet code, but with some minor changes to make it work
// witht the manager.

/**
 * This file contains the basic class and values for easily working with errors on the app.
 */

/**
 * Possible values of OperationError.type for identifying errors during general operations.
 */
export enum OperationErrorTypes {
  /**
   * There is no internet connection or connection to the Hypervisor.
   */
  NoConnection = 'NoConnection',
  /**
   * The user is not authorized. Normally means that the session is not valid.
   */
  Unauthorized = 'Unauthorized',
  /**
   * The error is not in the list of known errors that require special treatment. This does not
   * mean the error is rare or specially bad. Just showing the error msg should be enough.
   */
  Unknown = 'Unknown',
}

/**
 * Base object for working with errors throughout the application.
 */
export class OperationError {
  /**
   * Specific error type. Allows to know the cause of the error.
   */
  type: OperationErrorTypes;
  /**
   * Original error object from which this OperationError instance was created.
   */
  originalError: any;
  /**
   * Original, unprocessed, error msg.
   */
  originalServerErrorMsg: string;
  /**
   * Processed error msg, which can be passed to the 'translate' pipe to display it on the UI.
   */
  translatableErrorMsg: string;
}
