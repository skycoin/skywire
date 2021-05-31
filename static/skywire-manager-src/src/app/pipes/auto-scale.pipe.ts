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
  /**
   * If the data must be shown in bits (true) or bytes (false).
   */
  useBits: boolean;
}

/**
 * Allows to convert a bytes value to KB, MB, GB, etc. It considers 1024, and not 1000, a K.
 */
@Pipe({
  name: 'autoScale'
})
export class AutoScalePipe implements PipeTransform {
  private static readonly accumulatedMeasurements = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
  private static readonly measurementsPerSec = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s', 'PB/s', 'EB/s', 'ZB/s', 'YB/s'];
  private static readonly accumulatedMeasurementsInBits = ['b', 'Kb', 'Mb', 'Gb', 'Tb', 'Pb', 'Eb', 'Zb', 'Yb'];
  private static readonly measurementsPerSecInBits = ['b/s', 'Kb/s', 'Mb/s', 'Gb/s', 'Tb/s', 'Pb/s', 'Eb/s', 'Zb/s', 'Yb/s'];

  transform(value: any, params: AutoScalePipeParams): any {
    let useBytes = true;

    /**
     * Load the requested unit labels.
     */
    let measurements: String[];
    if (!params) {
      measurements = AutoScalePipe.accumulatedMeasurements;
    } else {
      if (params.showPerSecond) {
        if (params.useBits) {
          measurements = AutoScalePipe.measurementsPerSecInBits;
          useBytes = false;
        } else {
          measurements = AutoScalePipe.measurementsPerSec;
        }
      } else {
        if (params.useBits) {
          measurements = AutoScalePipe.accumulatedMeasurementsInBits;
          useBytes = false;
        } else {
          measurements = AutoScalePipe.accumulatedMeasurements;
        }
      }
    }

    // Calculate the number and the unit.
    let val = new BigNumber(value);
    if (!useBytes) {
      val = val.multipliedBy(8);
    }
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
