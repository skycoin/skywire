import { HttpClient } from '@angular/common/http';
import { Component } from '@angular/core';
import { ApiService, ResponseTypes } from 'src/app/services/api.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { RoutePreviewComponent, RoutePreviewData } from './route-preview/route-preview.component';
import { MatDialog } from '@angular/material/dialog';
import { UntypedFormControl, UntypedFormGroup } from '@angular/forms';

class NodeConnections {
  transports: TransportConnection[] = [];
  alreadyAddedSet = new Set<string>();
}

class TransportConnection {
  remotePk: string;
  transportId: string;
}

class NestedTransportConnection {
  level: number;
  nodePk: string;
  transportId: string;
  children: NestedTransportConnection[] = [];
  returnsToOrigin: boolean;
  withTransportToOrigin: boolean;
  currentRoute = '';
}

enum ValidationErrors {
  SameStartAndDestination,
  FinishNotOnMap,
  InvalidMaxHopsValue,
  NoDiscoveryData,
}

@Component({
  selector: 'app-transport-tools-page',
  templateUrl: './transport-tools-page.component.html',
  styleUrls: ['./transport-tools-page.component.scss'],
})
export class TransportToolsPageComponent {
  connections = new Map<string, NodeConnections>();

  startNode = '';
  destinationNode = '';
  visorConnections: NestedTransportConnection;
  maxLevels = 0;

  requestingData = true;

  validationError: ValidationErrors;
  validationErrors = ValidationErrors;

  completeRoutesFound = 0;
  completeRoutes: NestedTransportConnection[] = [];

  branches = 0;

  form: UntypedFormGroup;

  constructor(
    private dialog: MatDialog,
  ) {
    this.form = new UntypedFormGroup({
      discoveryData: new UntypedFormControl(''),
      startNode: new UntypedFormControl(''),
      finalNode: new UntypedFormControl(''),
      maxSteps: new UntypedFormControl(3),
    });
  }

  process() {
    this.requestingData = false;

    this.startNode = (this.form.get('startNode').value as string).trim().toLowerCase();
    this.destinationNode = (this.form.get('finalNode').value as string).trim().toLowerCase();

    if (this.startNode === this.destinationNode) {
      this.validationError = ValidationErrors.SameStartAndDestination;

      return;
    }

    //

    this.maxLevels = Number.parseInt(this.form.get('maxSteps').value, 10);
    if (isNaN(this.maxLevels) || this.maxLevels < 1) {
      this.validationError = ValidationErrors.InvalidMaxHopsValue;

      return;
    }

    //

    if (!this.form.get('discoveryData').value || (this.form.get('discoveryData').value as string).length < 5) {
      this.validationError = ValidationErrors.NoDiscoveryData;

      return;
    }

    const discoveryContent = JSON.parse(this.form.get('discoveryData').value);

    (discoveryContent as any[]).forEach(t => {
      this.addNodeToList(t, true);
      this.addNodeToList(t, false);
    });

    //

    if (!this.connections.get(this.destinationNode)) {
      this.validationError = ValidationErrors.FinishNotOnMap;

      return;
    }

    this.visorConnections = this.buildNestedConnections(this.destinationNode, '', new Set<string>(), 0, false, '');
  }

  private buildNestedConnections(nodePk: string, transportId: string, alreadyUsed: Set<string>, currentLevel: number, nextReturnsToOrigin: boolean, lastRoute: string): NestedTransportConnection {
    if (currentLevel > this.maxLevels) {
      return null;
    }

    alreadyUsed.add(nodePk);

    this.branches += 1;

    const element = new NestedTransportConnection();
    element.level = currentLevel;
    element.nodePk = nodePk;
    element.transportId = transportId;
    element.returnsToOrigin = nextReturnsToOrigin;
    element.withTransportToOrigin = this.connections.get(this.startNode).alreadyAddedSet.has(nodePk);
    element.currentRoute = lastRoute + nodePk + '/';
    element.children = [];

    if (nodePk === this.startNode) {
      this.completeRoutesFound += 1;
      this.completeRoutes.push(element);
    }

    nextReturnsToOrigin = nextReturnsToOrigin || nodePk === this.startNode;

    this.connections.get(nodePk).transports.forEach(e => {
      if (!alreadyUsed.has(e.remotePk)) {
        const data = this.buildNestedConnections(e.remotePk, e.transportId, alreadyUsed, currentLevel + 1, nextReturnsToOrigin, element.currentRoute);
        if (data) {
          element.children.push(data);
        }
      }
    });

    alreadyUsed.delete(nodePk);

    return element;
  }

  private addNodeToList(transport: any, processFirst: boolean) {
    const startIndex = processFirst ? 0 : 1;
    const endIndex = processFirst ? 1 : 0;

    const startPk = (transport.edges[startIndex] as string).toLowerCase();
    const endPk = (transport.edges[endIndex] as string).toLowerCase();

    if (!this.connections.has(startPk)) {
      this.connections.set(startPk, new NodeConnections());
    }
    const connections = this.connections.get(startPk);

    if (!connections.alreadyAddedSet.has(endPk)) {
      connections.transports.push({remotePk: endPk, transportId: transport.t_id});
      connections.alreadyAddedSet.add(endPk);
    }
  }

  openRouteDetails(elementData: NestedTransportConnection) {
    const data = new RoutePreviewData();
    data.startPk = this.startNode;
    data.destinationPk = this.destinationNode;
    data.PkForNewTransport = elementData.nodePk;
    data.route = elementData.currentRoute;
    data.connectionsFronStart = this.connections.get(this.startNode).alreadyAddedSet;

    RoutePreviewComponent.openDialog(this.dialog, data);
  }

  nodeTooltip(elementData: NestedTransportConnection): string {
    if (!elementData.returnsToOrigin) {
      if (!elementData.withTransportToOrigin) {
        return 'If you create a transport from ' + this.startNode +
          ' to this visor, you will have a route with ' + (elementData.level + 1) +
          ' total hops to ' + this.destinationNode + '.';
      } else {
        return 'The origin visor already has a transport to this visor. The transport allows a route with ' +
          (elementData.level + 1) + ' total hops to ' + this.destinationNode + '.';
      }
    } else {
      let response = 'Creating a transport to this visor would create a route that would have to return to the start visor before reaching the destination.';
      if (elementData.withTransportToOrigin) {
        response += ' Also, the origin visor already has a transport to this visor.';
      }

      return response;
    }
  }
}
