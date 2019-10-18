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
}
