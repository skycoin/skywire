import { Component, DoCheck, ElementRef, Input, IterableDiffers, ViewChild, AfterViewInit, IterableDiffer, OnDestroy } from '@angular/core';
import { Chart } from 'chart.js';

/**
 * Line chart used for showing how much data has been uploaded/downloaded.
 */
@Component({
  selector: 'app-line-chart',
  templateUrl: './line-chart.component.html',
  styleUrls: ['./line-chart.component.scss']
})
export class LineChartComponent implements AfterViewInit, DoCheck, OnDestroy {
  // Margin at the top of the chart. The max value will be this many pixels from the top.
  public static topInternalMargin = 5;

  @ViewChild('chart') chartElement: ElementRef;
  @Input() data: number[];
  @Input() height = 100;
  @Input() animated = false;

  // Max and min values that the chart sill show. If not set, the chart will calculate the
  // values automatically.
  @Input() min: number = undefined;
  @Input() max: number = undefined;

  chart: any;

  private differ: IterableDiffer<unknown>;

  constructor(
    differs: IterableDiffers,
  ) {
    // Create the object used for checking if the "data" var has been updated.
    this.differ = differs.find([]).create(null);
  }

  ngAfterViewInit() {
    // The chart shows the values of the "data" var and most of the visual
    // elements are removed.
    this.chart = new Chart(this.chartElement.nativeElement, {
      type: 'line',
      data: {
        labels: Array.from(Array(this.data.length).keys()),
        datasets: [{
          data: this.data,
          backgroundColor: ['rgba(10, 15, 22, 0.4)'],
          borderColor: ['rgba(10, 15, 22, 0.4)'],
          borderWidth: 1,
        }],
      },
      options: {
        maintainAspectRatio: false,
        events: [],
        legend: { display: false },
        tooltips: { enabled: false },
        scales: {
          yAxes: [{
            display: false,
            ticks: {
              suggestedMin: 0,
            },
          }],
          xAxes: [{ display: false }],
        },
        elements: { point: { radius: 0 } },
        layout: {
          padding: {
              left: 0,
              right: 0,
              top: LineChartComponent.topInternalMargin,
              bottom: 0
          }
        },
      },
    });

    // Update the max and min values, if set.
    if (this.min !== undefined && this.max !== undefined) {
      this.updateMinAndMax();
      this.chart.update(0);
    }
  }

  ngDoCheck() {
    const changes = this.differ.diff(this.data);

    // Update the chart only when the values of the "data" var change.
    if (changes && this.chart) {
      if (this.min !== undefined && this.max !== undefined) {
        this.updateMinAndMax();
      }

      if (this.animated) {
        this.chart.update();
      } else {
        this.chart.update(0);
      }
    }
  }

  ngOnDestroy() {
    if (this.chart) {
      this.chart.destroy();
    }
  }

  /**
   * Updates the max and min values the chart shows.
   */
  private updateMinAndMax() {
    this.chart.options.scales = {
      yAxes: [{
          display: false,
          ticks: {
            min: this.min,
            max: this.max,
          },
      }],
      xAxes: [{ display: false }],
    };
  }
}
