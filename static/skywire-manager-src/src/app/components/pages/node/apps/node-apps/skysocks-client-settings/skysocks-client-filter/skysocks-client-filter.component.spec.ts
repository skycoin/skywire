import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { SkysocksClientFilterComponent } from './skysocks-client-filter.component';

describe('SkysocksClientFilterComponent', () => {
  let component: SkysocksClientFilterComponent;
  let fixture: ComponentFixture<SkysocksClientFilterComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ SkysocksClientFilterComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(SkysocksClientFilterComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
