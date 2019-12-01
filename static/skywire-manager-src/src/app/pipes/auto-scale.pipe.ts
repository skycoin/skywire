import { Pipe, PipeTransform } from '@angular/core';

export class AutoScalePipeParams {
  showValue: boolean;
  showUnit: boolean;
}

@Pipe({
  name: 'autoScale'
})
export class AutoScalePipe implements PipeTransform {

  transform(value: any, params: AutoScalePipeParams): any {
    let result = '';

    if (!params || !!params.showValue) {
      result = (value / 128).toFixed(2);
    }
    if (!params || (!!params.showValue && !!params.showUnit)) {
      result = result + ' ';
    }
    if (!params || !!params.showUnit) {
      result = result + 'Kb';
    }

    return result;
  }

}
