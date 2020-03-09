export enum TimeRepresentations {
  Seconds, Minutes, Hours, Days, Weeks
}

/**
 * Elemets for showing in the UI an amount of elapsed time.
 */
export class ElapsedTime {
  /**
   * Explanation of the value in elapsedTime.
   */
  timeRepresentation: TimeRepresentations;
  /**
   * Amount of time to show in the UI.
   */
  elapsedTime: string;
  /**
   * Total time in minutes.
   */
  totalMinutes: string;
  /**
   * Var from the translation file to be use to show the elapsed time in the UI.
   */
  translationVarName: string;
}

/**
 * Helper functions for making calculations with time.
 */
export default class TimeUtils {
  /**
   * Calculates the best way to display in the UI an amount of elapsed time.
   */
  static getElapsedTime(elapsedSeconds: number): ElapsedTime {
    const response = new ElapsedTime();
    response.timeRepresentation = TimeRepresentations.Seconds;
    response.totalMinutes = Math.floor(elapsedSeconds / 60).toString();
    response.translationVarName = 'second';

    let divider = 1;

    if (elapsedSeconds >= 60 && elapsedSeconds < 3600) {
      response.timeRepresentation = TimeRepresentations.Minutes;
      divider = 60;
      response.translationVarName = 'minute';
    } else if (elapsedSeconds >= 3600 && elapsedSeconds < 86400) {
      response.timeRepresentation = TimeRepresentations.Hours;
      divider = 3600;
      response.translationVarName = 'hour';
    } else if (elapsedSeconds >= 86400 && elapsedSeconds < 604800) {
      response.timeRepresentation = TimeRepresentations.Days;
      divider = 86400;
      response.translationVarName = 'day';
    } else if (elapsedSeconds >= 604800) {
      response.timeRepresentation = TimeRepresentations.Weeks;
      divider = 604800;
      response.translationVarName = 'week';
    }

    const elapsedTime = Math.floor(elapsedSeconds / divider);
    response.elapsedTime = elapsedTime.toString();

    if (response.timeRepresentation === TimeRepresentations.Seconds || elapsedTime > 1) {
      response.translationVarName = response.translationVarName + 's';
    }

    return response;
  }
}
