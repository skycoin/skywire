export enum TimeRepresentations {
  Seconds, Minutes, Hours, Days, Weeks
}

export class ElapsedTime {
  timeRepresentation: TimeRepresentations;
  elapsedTime: string;
  totalMinutes: string;
  translationVarName: string;
}

export default class TimeUtils {
  static getElapsedTimeElements(elapsedSeconds: number): string[] {
    if (elapsedSeconds < 60) {
      return ['seconds', '', ''];
    } else if (elapsedSeconds >= 60 && elapsedSeconds < 120) {
      return ['minute', '', ''];
    } else if (elapsedSeconds >= 120 && elapsedSeconds < 3600) {
      return ['minutes', Math.floor(elapsedSeconds / 60).toString(), ''];
    } else if (elapsedSeconds >= 3600 && elapsedSeconds < 7200) {
      return ['hour', '', Math.floor(elapsedSeconds / 60).toString()];
    } else if (elapsedSeconds >= 7200 && elapsedSeconds < 86400) {
      return ['hours', Math.floor(elapsedSeconds / 3600).toString(), Math.floor(elapsedSeconds / 60).toString()];
    } else if (elapsedSeconds >= 86400 && elapsedSeconds < 172800) {
      return ['day', '', Math.floor(elapsedSeconds / 60).toString()];
    } else if (elapsedSeconds >= 172800 && elapsedSeconds < 604800) {
      return ['days', Math.floor(elapsedSeconds / 86400).toString(), Math.floor(elapsedSeconds / 60).toString()];
    } else if (elapsedSeconds >= 604800 && elapsedSeconds < 1209600) {
      return ['week', '', Math.floor(elapsedSeconds / 60).toString()];
    } else {
      return ['weeks', Math.floor(elapsedSeconds / 604800).toString(), Math.floor(elapsedSeconds / 60).toString()];
    }
  }

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
