<?php

namespace App\Http\Controllers;

use App\Domain\Entities\DnsRecord;
use App\Facades\App;
use App\Facades\Dns;
use App\Http\Request;
use App\Http\Response;
use App\Support\AuditLogger;

class DnsController extends Controller
{
    public function index(Request $request): Response
    {
        $domains = $this->isAdmin()
            ? App::domains()->all()
            : App::domains()->findByUserId($this->currentUserId());

        foreach ($domains as $domain) {
            $site = App::sites()->find($domain->siteId);
            $domain->siteDomain = $site ? $site->domain : 'Unknown';
        }

        return $this->view('pages/dns/index', [
            'title' => 'DNS Management',
            'domains' => $domains,
        ]);
    }

    public function show(Request $request, int $id): Response
    {
        $domain = App::domains()->find($id);
        if (!$domain) {
            return new Response('Domain not found', 404);
        }

        try {
            $this->authorizeOwnedDomainId($id);
        } catch (\RuntimeException $e) {
            return new Response($e->getMessage(), 403);
        }

        return $this->view('pages/dns/show', [
            'title' => 'DNS Records - ' . $domain->name,
            'domain' => $domain,
            'records' => App::dnsRecords()->findByDomainId($id),
        ]);
    }

    public function create(Request $request): Response
    {
        $sites = $this->isAdmin()
            ? App::sites()->all()
            : App::sites()->findByUserId($this->currentUserId());

        return $this->view('pages/dns/create', [
            'title' => 'Create DNS Zone',
            'sites' => $sites,
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $siteId = (int) $request->post('site_id');
            $this->authorizeOwnedSiteId($siteId);

            $domainName = $request->post('domain_name');
            $serverIp = $request->post('server_ip');

            App::setupDnsZoneService()->execute(
                siteId: $siteId,
                domainName: $domainName,
                serverIp: $serverIp
            );

            AuditLogger::logCreated('dns_zone', $domainName, [
                'site_id' => $siteId,
                'server_ip' => $serverIp,
            ]);

            if ($request->isHtmx()) {
                return new Response($this->successAlert('DNS zone created successfully! Redirecting...'));
            }

            return $this->redirect('/dns');
        } catch (\Exception $e) {
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($e->getMessage()), 400);
            }

            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function addRecord(Request $request, int $domainId): Response
    {
        try {
            $this->authorizeOwnedDomainId($domainId);

            $record = App::dnsRecords()->create(new DnsRecord(
                domainId: $domainId,
                name: $request->post('name'),
                type: $request->post('type'),
                content: $request->post('content'),
                ttl: (int) $request->post('ttl', 3600),
                priority: $request->post('priority') ? (int) $request->post('priority') : null
            ));

            Dns::getInstance()->addRecord($record);

            AuditLogger::logCreated('dns_record', "{$record->name} ({$record->type})", [
                'domain_id' => $domainId,
                'type' => $record->type,
                'content' => $record->content,
            ]);

            return $this->redirect("/dns/{$domainId}");
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function deleteRecord(Request $request, int $domainId, int $recordId): Response
    {
        try {
            $this->authorizeOwnedDomainId($domainId);

            $record = App::dnsRecords()->find($recordId);
            if (!$record || (int) $record->domainId !== $domainId) {
                throw new \Exception('DNS record not found');
            }

            AuditLogger::logDeleted('dns_record', "{$record->name} ({$record->type})", [
                'record_id' => $recordId,
                'domain_id' => $domainId,
                'content' => $record->content,
            ]);

            Dns::getInstance()->deleteRecord($record);
            App::dnsRecords()->delete($recordId);

            return $this->redirect("/dns/{$domainId}");
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
