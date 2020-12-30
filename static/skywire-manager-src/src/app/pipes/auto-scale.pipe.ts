import { Pipe, PipeTransform } from '@angular/core';
import { BigNumber } from 'bignumber.js';

export class AutoScalePipeParams {
  showValue: boolean;
  showUnit: boolean;
  showPerSecond: boolean;
  limitDecimals: boolean;
}

@Pipe({
  name: 'autoScale'
})
export class AutoScalePipe implements PipeTransform {

  transform(value: any, params: AutoScalePipeParams): any {
    const accumulatedMeasurements = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
    const measurementsPerSec = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s', 'PB/s', 'EB/s', 'ZB/s', 'YB/s'];

    const measurements = !params || !params.showPerSecond ? accumulatedMeasurements : measurementsPerSec;

    let val = new BigNumber(value);
    let measurement = measurements[0];
    let currentIndex = 0;
    while (val.dividedBy(1024).isGreaterThan(1)) {
      val = val.dividedBy(1024);
      currentIndex += 1;
      measurement = measurements[currentIndex];
    }

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
