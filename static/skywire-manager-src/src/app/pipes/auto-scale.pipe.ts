import { Pipe, PipeTransform } from '@angular/core';
import { BigNumber } from 'bignumber.js';

/**
 * Params AutoScalePipe can receive.
 */
class AutoScalePipeParams {
  /**
   * If true, the numeric value will be shown.
   */
  showValue: boolean;
  /**
   * If true, the unit will be shown.
   */
  showUnit: boolean;
  /**
   * If true, the unit will be in data per second (like KB/s instead of KB).
   */
  showPerSecond: boolean;
  /**
   * If true, the numeric value will have at most 1 decimal.
   */
  limitDecimals: boolean;
}

/**
 * Allows to convert a bytes value to KB, MB, GB, etc. It considers 1024, and not 1000, a K.
 */
@Pipe({
  name: 'autoScale'
})
export class AutoScalePipe implements PipeTransform {

  transform(value: any, params: AutoScalePipeParams): any {
    const accumulatedMeasurements = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
    const measurementsPerSec = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s', 'PB/s', 'EB/s', 'ZB/s', 'YB/s'];
    /**
     * Units to use, as per requested.
     */
    const measurements = !params || !params.showPerSecond ? accumulatedMeasurements : measurementsPerSec;

    // Calculate the number and the unit.
    let val = new BigNumber(value);
    let measurement = measurements[0];
    let currentIndex = 0;
    while (val.dividedBy(1024).isGreaterThan(1)) {
      val = val.dividedBy(1024);
      currentIndex += 1;
      measurement = measurements[currentIndex];
    }

    // Add the requested parts.
    let result = '';
    if (!params || !!params.showValue) {
      if (params && params.limitDecimals) {
        result = (new BigNumber(val)).decimalPlaces(1).toString();
      } else {
        result = val.toFixed(2);
      }
    }
    if (!params || (!!params.showValue && !!params.showUnit)) {
      result = result + ' ';
    }
    if (!params || !!params.showUnit) {
      result = result + measurement;
    }

    return result;
  }
}
