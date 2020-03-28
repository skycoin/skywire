import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { SkysocksClientSettingsComponent } from './skysocks-client-settings.component';

describe('SkysocksClientSettingsComponent', () => {
  let component: SkysocksClientSettingsComponent;
  let fixture: ComponentFixture<SkysocksClientSettingsComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ SkysocksClientSettingsComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(SkysocksClientSettingsComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
